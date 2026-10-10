package toad

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

const validationExtension = "x-toad-validation"

type validationRule struct {
	raw, name, param string
	hasParam, or     bool
}

type validationPresence struct {
	parent *openapi3.Schema
	name   string
	typ    reflect.Type
	quoted bool
	ref    *openapi3.SchemaRef
}

// Delimiters are split before decoding the validator's escaped parameter values.
// In particular, a quoted oneof choice does not escape a literal comma or pipe.
func parseValidation(tag string) ([]validationRule, error) {
	if tag == "" || tag == "-" {
		return nil, nil
	}
	var rules []validationRule
	for _, raw := range strings.Split(tag, ",") {
		if raw == "" {
			return nil, fmt.Errorf("validation rule %q: empty rule", raw)
		}
		name, param, hasParam := strings.Cut(raw, "=")
		rule := validationRule{raw: raw, name: name, param: decodeValidationParam(param), hasParam: hasParam, or: strings.Contains(raw, "|")}
		for _, alternative := range strings.Split(raw, "|") {
			name, _, _ := strings.Cut(alternative, "=")
			if name == "" {
				return nil, fmt.Errorf("validation rule %q: empty alternative", raw)
			}
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func decodeValidationParam(param string) string {
	return strings.ReplaceAll(strings.ReplaceAll(param, "0x2C", ","), "0x7C", "|")
}

func underlying(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func schemaRef(s *openapi3.Schema) *openapi3.SchemaRef { return &openapi3.SchemaRef{Value: s} }

func nullSchema() *openapi3.SchemaRef {
	return schemaRef(&openapi3.Schema{Type: &openapi3.Types{"object"}, Nullable: true, Enum: []any{nil}})
}

func conjunction(a, b *openapi3.SchemaRef) *openapi3.SchemaRef {
	return schemaRef(&openapi3.Schema{AllOf: openapi3.SchemaRefs{a, b}})
}

func (r *schemaRegistry) fieldSchema(t reflect.Type, validated, quoted bool) (*openapi3.SchemaRef, error) {
	// Check custom encoders even when the JSON string option changes the shape.
	ref, err := r.schemaIn(t, validated)
	if err != nil || !quoted {
		return ref, err
	}
	s := openapi3.NewStringSchema()
	s.Nullable = t.Kind() == reflect.Pointer
	return schemaRef(s), nil
}

func (r *schemaRegistry) validatedField(t reflect.Type, tag string, quoted bool) (*openapi3.SchemaRef, bool, error) {
	rules, err := parseValidation(tag)
	if err != nil {
		return nil, false, err
	}
	if tag == "-" {
		ref, err := r.fieldSchema(t, false, quoted)
		return ref, false, err
	}
	if err := checkValidationStructure(t, rules); err != nil {
		return nil, false, err
	}
	ref, unsupported, err := r.validationSchema(t, rules, quoted)
	if err != nil {
		return nil, false, err
	}
	required := unconditionalRequired(t, rules)
	if len(unsupported) > 0 {
		ref = schemaRef(&openapi3.Schema{AllOf: openapi3.SchemaRefs{ref}, Extensions: map[string]any{
			validationExtension: map[string]any{"tags": tag, "unsupported": unsupported},
		}})
	}
	return ref, required, nil
}

// Delay zero-value inference until recursive models are complete. Iterate to a
// fixed point because an omitted nested object may omit its own required fields.
// Unknown rules prevent inference; explicit unconditional required survives.
func (r *schemaRegistry) inferValidationPresence() {
	for changed := true; changed; {
		changed = false
		for _, field := range r.presence {
			if slices.Contains(field.parent.Required, field.name) || hasUnsupported(field.ref, make(map[*openapi3.Schema]bool)) {
				continue
			}
			data, err := json.Marshal(reflect.Zero(field.typ).Interface())
			if err != nil {
				continue
			}
			var zero any
			if json.Unmarshal(data, &zero) != nil {
				continue
			}
			if field.quoted && field.typ.Kind() != reflect.Pointer {
				zero = string(data)
			}
			if !acceptsValidationZero(field.ref, zero) {
				field.parent.Required = append(field.parent.Required, field.name)
				changed = true
			}
		}
	}
	for _, field := range r.presence {
		slices.Sort(field.parent.Required)
	}
}

func unconditionalRequired(t reflect.Type, rules []validationRule) bool {
	for _, rule := range rules {
		if rule.or {
			continue
		}
		switch rule.name {
		case "required":
			return true
		case "omitnil":
			if t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
				return false
			}
		case "omitempty", "dive", "omitzero", "structonly", "nostructlevel":
			return false
		case "isdefault":
			if t.Kind() == reflect.Pointer {
				return false
			}
		}
	}
	return false
}

func rawRules(rules []validationRule) string {
	parts := make([]string, len(rules))
	for i, rule := range rules {
		parts[i] = rule.raw
	}
	return strings.Join(parts, ",")
}

// Validate structure even in groups withheld from translation by a control tag.
func checkValidationStructure(t reflect.Type, rules []validationRule) error {
	for i, rule := range rules {
		fail := func(message string) error { return fmt.Errorf("validation rule %q: %s", rule.raw, message) }
		if rule.or {
			for _, alternative := range strings.Split(rule.raw, "|") {
				parsed, _ := parseValidation(alternative)
				if _, _, err := validationConstraint(t, parsed[0], false); err != nil {
					return fail(err.Error())
				}
			}
			continue
		}
		switch rule.name {
		case "omitempty", "omitnil", "omitzero", "structonly", "nostructlevel":
			if rule.hasParam {
				return fail("does not take a parameter")
			}
		case "dive":
			base := underlying(t)
			if rule.hasParam {
				return fail("does not take a parameter")
			}
			if base.Kind() != reflect.Slice && base.Kind() != reflect.Array && base.Kind() != reflect.Map {
				return fail("requires a collection")
			}
			rest := rules[i+1:]
			if len(rest) > 0 && rest[0].name == "keys" && !rest[0].or {
				if base.Kind() != reflect.Map || rest[0].hasParam {
					return fail("keys requires a map dive")
				}
				end := -1
				for j := 1; j < len(rest); j++ {
					if rest[j].name == "endkeys" && !rest[j].or {
						end = j
						break
					}
				}
				if end < 0 {
					return fail("keys requires endkeys")
				}
				if rest[end].hasParam {
					return fail("endkeys does not take a parameter")
				}
				if err := checkValidationStructure(base.Key(), rest[1:end]); err != nil {
					return fmt.Errorf("map key: %w", err)
				}
				rest = rest[end+1:]
			}
			return checkValidationStructure(base.Elem(), rest)
		case "keys", "endkeys":
			return fail("requires a matching map dive/keys group")
		default:
			if _, _, err := validationConstraint(t, rule, false); err != nil {
				return fail(err.Error())
			}
		}
	}
	return nil
}

func (r *schemaRegistry) validationSchema(t reflect.Type, rules []validationRule, quoted bool) (*openapi3.SchemaRef, []string, error) {
	if len(rules) == 0 {
		ref, err := r.fieldSchema(t, true, quoted)
		return ref, nil, err
	}
	rule, rest := rules[0], rules[1:]
	base := underlying(t)
	fail := func(err error) (*openapi3.SchemaRef, []string, error) {
		return nil, nil, fmt.Errorf("validation rule %q: %w", rule.raw, err)
	}
	if !rule.or {
		switch rule.name {
		case "omitempty", "omitnil":
			if rule.hasParam {
				return fail(fmt.Errorf("does not take a parameter"))
			}
			empty := emptyValidationValue(t, rule.name == "omitnil")
			if empty == nil {
				// Zero structs/arrays cannot in general be matched on the JSON wire.
				ref, err := r.fieldSchema(t, false, quoted)
				return ref, []string{rawRules(rules)}, err
			}
			ref, unsupported, err := r.validationSchema(t, rest, quoted)
			if err != nil {
				return nil, nil, err
			}
			if rule.name == "omitnil" && t.Kind() != reflect.Pointer && base.Kind() != reflect.Slice && base.Kind() != reflect.Map {
				return ref, unsupported, nil // omitnil has no effect on scalar values.
			}
			if quoted && t.Kind() != reflect.Pointer && rule.name == "omitempty" {
				ref, err := r.fieldSchema(t, false, true)
				return ref, []string{rawRules(rules)}, err
			}
			return schemaRef(&openapi3.Schema{AnyOf: openapi3.SchemaRefs{empty, ref}}), unsupported, nil
		case "dive":
			if rule.hasParam {
				return fail(fmt.Errorf("does not take a parameter"))
			}
			if base.Kind() != reflect.Slice && base.Kind() != reflect.Array && base.Kind() != reflect.Map {
				return fail(fmt.Errorf("requires a collection, got %s", t))
			}
			var unsupported []string
			if len(rest) > 0 && rest[0].name == "keys" && !rest[0].or {
				if base.Kind() != reflect.Map || rest[0].hasParam {
					return fail(fmt.Errorf("keys requires a map dive"))
				}
				end := -1
				for i := 1; i < len(rest); i++ {
					if rest[i].name == "endkeys" && !rest[i].or {
						end = i
						break
					}
				}
				if end < 0 {
					return fail(fmt.Errorf("keys requires endkeys"))
				}
				if rest[end].hasParam {
					return fail(fmt.Errorf("endkeys does not take a parameter"))
				}
				// Parse key structure and supported parameters, but never constrain keys.
				if _, _, err := r.validationSchema(base.Key(), rest[1:end], false); err != nil {
					return nil, nil, fmt.Errorf("map key: %w", err)
				}
				unsupported = append(unsupported, rawRules(rest[:end+1]))
				rest = rest[end+1:]
			}
			item, childUnsupported, err := r.validationSchema(base.Elem(), rest, false)
			if err != nil {
				return nil, nil, fmt.Errorf("element: %w", err)
			}
			unsupported = append(unsupported, childUnsupported...)
			ref, err := r.fieldSchema(t, false, quoted)
			if err != nil {
				return nil, nil, err
			}
			if base.Kind() == reflect.Slice && base.Elem().Kind() == reflect.Uint8 {
				return ref, []string{rawRules(rules)}, nil // base64 is not a JSON array.
			}
			var s *openapi3.Schema
			if base.Kind() == reflect.Map {
				s = openapi3.NewObjectSchema()
				s.AdditionalProperties = openapi3.AdditionalProperties{Schema: item}
			} else {
				s = openapi3.NewArraySchema()
				s.Items = item
			}
			s.Nullable = t.Kind() != reflect.Pointer && base.Kind() != reflect.Array
			return conjunction(ref, schemaRef(s)), unsupported, nil
		case "keys", "endkeys":
			return fail(fmt.Errorf("requires a matching map dive/keys group"))
		case "omitzero", "structonly", "nostructlevel", "-":
			// These controls change subsequent validation or automatic traversal.
			ref, err := r.fieldSchema(t, false, quoted)
			return ref, []string{rawRules(rules)}, err
		case "isdefault":
			if t.Kind() == reflect.Pointer {
				// Validator returns immediately for a nil pointer with isdefault,
				// bypassing the remaining rules and nested traversal.
				ref, err := r.fieldSchema(t, false, quoted)
				return ref, []string{rawRules(rules)}, err
			}
		}
	}
	var constraint *openapi3.SchemaRef
	var supported bool
	var err error
	if rule.or {
		// Check malformed supported parameters, but keep the entire OR expression.
		for _, alternative := range strings.Split(rule.raw, "|") {
			parsed, _ := parseValidation(alternative)
			if _, _, err = validationConstraint(t, parsed[0], quoted); err != nil {
				return fail(err)
			}
		}
	} else {
		constraint, supported, err = validationConstraint(t, rule, quoted)
		if err != nil {
			return fail(err)
		}
	}
	ref, unsupported, err := r.validationSchema(t, rest, quoted)
	if err != nil {
		return nil, nil, err
	}
	if !supported {
		return ref, append([]string{rule.raw}, unsupported...), nil
	}
	return conjunction(ref, constraint), unsupported, nil
}

func emptyValidationValue(t reflect.Type, nilOnly bool) *openapi3.SchemaRef {
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		return nullSchema()
	}
	if nilOnly {
		return schemaRef(&openapi3.Schema{})
	}
	switch t.Kind() {
	case reflect.String:
		return schemaRef(openapi3.NewStringSchema().WithEnum(""))
	case reflect.Bool:
		return schemaRef(openapi3.NewBoolSchema().WithEnum(false))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return schemaRef(openapi3.NewFloat64Schema().WithEnum(float64(0)))
	}
	return nil
}

var oneofChoices = regexp.MustCompile(`'[^']*'|\S+`)

func validationConstraint(t reflect.Type, rule validationRule, quoted bool) (*openapi3.SchemaRef, bool, error) {
	base := underlying(t)
	kind := base.Kind()
	bad := func(err error) (*openapi3.SchemaRef, bool, error) { return nil, false, err }
	unsupported := func() (*openapi3.SchemaRef, bool, error) { return nil, false, nil }
	if rule.name == "required" || rule.name == "email" || rule.name == "uuid" {
		if rule.hasParam {
			return bad(fmt.Errorf("does not take a parameter"))
		}
	}
	if rule.name == "required" {
		if t.Kind() == reflect.Pointer || kind == reflect.Map || kind == reflect.Slice {
			return schemaRef(&openapi3.Schema{Not: nullSchema()}), true, nil
		}
		if quoted {
			return unsupported()
		}
		switch kind {
		case reflect.String:
			return schemaRef(openapi3.NewStringSchema().WithMinLength(1)), true, nil
		case reflect.Bool:
			return schemaRef(openapi3.NewBoolSchema().WithEnum(true)), true, nil
		case reflect.Struct, reflect.Array:
			return unsupported()
		default:
			return schemaRef(&openapi3.Schema{Not: schemaRef(openapi3.NewFloat64Schema().WithEnum(float64(0)))}), true, nil
		}
	}
	if rule.name == "email" || rule.name == "uuid" {
		if kind != reflect.String || quoted {
			return unsupported()
		}
		return schemaRef(openapi3.NewStringSchema().WithFormat(rule.name)), true, nil
	}
	if rule.name == "oneof" {
		if !rule.hasParam || rule.param == "" {
			return bad(fmt.Errorf("requires choices"))
		}
		var values []any
		for _, token := range oneofChoices.FindAllString(rule.param, -1) {
			token = strings.ReplaceAll(token, "'", "") // Matches validator v10's quote handling.
			switch kind {
			case reflect.String:
				values = append(values, token)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				n, err := strconv.ParseInt(token, 10, base.Bits())
				if err != nil || strconv.FormatInt(n, 10) != token || !exactInteger(big.NewInt(n)) {
					return unsupported()
				}
				values = append(values, float64(n))
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				n, err := strconv.ParseUint(token, 10, base.Bits())
				if err != nil || strconv.FormatUint(n, 10) != token || !exactInteger(new(big.Int).SetUint64(n)) {
					return unsupported()
				}
				values = append(values, float64(n))
			default:
				return unsupported()
			}
		}
		if quoted || len(values) == 0 {
			return unsupported()
		}
		return schemaRef(&openapi3.Schema{Enum: values}), true, nil
	}
	switch rule.name {
	case "min", "max", "len", "gte", "lte", "gt", "lt":
	default:
		return unsupported()
	}
	if !rule.hasParam || rule.param == "" {
		return bad(fmt.Errorf("requires a numeric parameter"))
	}
	if kind == reflect.String || kind == reflect.Slice || kind == reflect.Array || kind == reflect.Map {
		n, err := strconv.ParseInt(rule.param, 0, 64)
		if err != nil {
			return bad(err)
		}
		if rule.name != "min" && rule.name != "max" && rule.name != "len" {
			return unsupported()
		}
		if quoted || kind == reflect.Slice && base.Elem().Kind() == reflect.Uint8 {
			return unsupported()
		}
		if n < 0 {
			return unsupported()
		} // OpenAPI count bounds are unsigned.
		var s *openapi3.Schema
		nn := uint64(n)
		minimum, maximum := rule.name != "max", rule.name != "min"
		switch kind {
		case reflect.String:
			s = openapi3.NewStringSchema()
			if minimum {
				s.MinLength = nn
			}
			if maximum {
				s.MaxLength = &nn
			}
		case reflect.Map:
			s = openapi3.NewObjectSchema()
			if minimum {
				s.MinProps = nn
			}
			if maximum {
				s.MaxProps = &nn
			}
		default:
			s = openapi3.NewArraySchema()
			s.Items = schemaRef(&openapi3.Schema{Nullable: true})
			if minimum {
				s.MinItems = nn
			}
			if maximum {
				s.MaxItems = &nn
			}
		}
		// Nil collections have length zero; nil pointers fail non-control rules.
		s.Nullable = t.Kind() != reflect.Pointer && (kind == reflect.Slice || kind == reflect.Map) && (!minimum || n == 0)
		return schemaRef(s), true, nil
	}
	n, exact, err := validationNumber(base, rule.param)
	if err != nil {
		return bad(err)
	}
	if !exact || quoted {
		return unsupported()
	}
	s := openapi3.NewFloat64Schema()
	switch rule.name {
	case "min", "gte", "gt":
		s.Min = &n
		s.ExclusiveMin = rule.name == "gt"
	case "max", "lte", "lt":
		s.Max = &n
		s.ExclusiveMax = rule.name == "lt"
	case "len":
		s.Enum = []any{n}
	}
	return schemaRef(s), true, nil
}

func exactInteger(n *big.Int) bool {
	// Beyond this range, neighboring integers can collapse onto the same float,
	// even when the parameter itself happens to be exactly representable.
	const largestSafeInteger = 1<<53 - 1
	return n.IsInt64() && n.Int64() >= -largestSafeInteger && n.Int64() <= largestSafeInteger
}

// kin-openapi represents numeric bounds and checks numeric enums as float64.
// Never silently round an integer bound or accept a noncanonical integer choice.
func validationNumber(t reflect.Type, param string) (float64, bool, error) {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(param, 0, 64)
		if t == reflect.TypeFor[time.Duration]() && err != nil {
			var duration time.Duration
			duration, err = time.ParseDuration(param)
			n = int64(duration)
		}
		return float64(n), exactInteger(big.NewInt(n)), err
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(param, 0, 64)
		return float64(n), exactInteger(new(big.Int).SetUint64(n)), err
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(param, t.Bits())
		return n, !math.IsInf(n, 0) && !math.IsNaN(n), err
	default:
		return 0, false, nil
	}
}

func hasUnsupported(ref *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) bool {
	if ref == nil || ref.Value == nil || seen[ref.Value] {
		return false
	}
	s := ref.Value
	seen[s] = true
	if s.Extensions[validationExtension] != nil {
		return true
	}
	for _, refs := range []openapi3.SchemaRefs{s.AllOf, s.AnyOf, s.OneOf} {
		for _, child := range refs {
			if hasUnsupported(child, seen) {
				return true
			}
		}
	}
	for _, child := range s.Properties {
		if hasUnsupported(child, seen) {
			return true
		}
	}
	return hasUnsupported(s.Items, seen) || hasUnsupported(s.AdditionalProperties.Schema, seen)
}

// Format annotations are deliberately not registered as global kin-openapi
// validators. For presence inference we only need to know that zero is invalid.
func acceptsValidationZero(ref *openapi3.SchemaRef, value any) bool {
	s := ref.Value
	if value == nil {
		return s.VisitJSON(nil) == nil
	}
	if value == "" && (s.Format == "email" || s.Format == "uuid") {
		return false
	}
	for _, child := range s.AllOf {
		if !acceptsValidationZero(child, value) {
			return false
		}
	}
	if len(s.AnyOf) > 0 {
		accepted := false
		for _, child := range s.AnyOf {
			accepted = accepted || acceptsValidationZero(child, value)
		}
		if !accepted {
			return false
		}
	}
	if object, ok := value.(map[string]any); ok {
		for name, child := range s.Properties {
			if v, exists := object[name]; exists && !acceptsValidationZero(child, v) {
				return false
			}
		}
	}
	if items, ok := value.([]any); ok && s.Items != nil {
		for _, v := range items {
			if !acceptsValidationZero(s.Items, v) {
				return false
			}
		}
	}
	return s.VisitJSON(value) == nil
}
