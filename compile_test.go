package toad_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Build fixtures against the current module with the same toolchain as this
// test. Failed builds must identify the builder method, not an unrelated setup error.
func TestHandlerCompilationContracts(t *testing.T) {
	const preamble = `package fixture
import (
 "net/http"
 "github.com/stephenhillier/toad"
)
type Input struct { Name string }
type OtherInput struct { Name string }
type Output struct { ID int }
type OtherOutput struct { ID int }
func ordinary(http.ResponseWriter, *http.Request) {}
func ordinaryBody(http.ResponseWriter, *http.Request, Input) {}
func managed(*http.Request) (Output, error) { return Output{}, nil }
func managedBody(*http.Request, Input) (Output, error) { return Output{}, nil }
func wrongBody(*http.Request, OtherInput) (Output, error) { return Output{}, nil }
func wrongOutput(*http.Request, Input) (OtherOutput, error) { return OtherOutput{}, nil }
func check(*http.Request, Input) error { return nil }
func wrongCheck(*http.Request, OtherInput) error { return nil }
func register() {
 api := toad.NewApi(http.NewServeMux())
`
	positive := `
 api.Route("POST /adoption").DescribeBody(Input{}).DescribeQuery(struct{ Search string }{}).DescribeResponse(201, Output{}).Title("adoption").Description("existing").HandlerFunc(ordinary)
 api.Route("DELETE /adoption").DescribeResponse(204, nil).Handler(http.HandlerFunc(ordinary))
 api.Route("GET /ordinary").Title("ordinary").Description("ordinary").HandlerFunc(ordinary)
 api.Route("POST /ordinary").Body(Input{}).Validator(check).Title("body").Description("body").HandlerFunc(ordinaryBody)
 api.Route("GET /explicit").Response(200, Output{}).Title("explicit").Description("explicit").HandlerFunc(managed)
 api.Route("POST /explicit").Body(Input{}).Validator(check).Response(201, Output{}).Validator(check).Title("body").Description("body").HandlerFunc(managedBody)
 api.Route("PUT /explicit").Response(201, Output{}).Body(Input{}).Validator(check).Title("body").Description("body").HandlerFunc(managedBody)
`
	type compilationFixture struct {
		name, source string
		valid        bool
	}
	fixtures := []compilationFixture{
		{"valid signatures", positive, true},
		{"ordinary rejects managed", `api.Route("GET /test").HandlerFunc(managed)`, false},
		{"ordinary body rejects managed", `api.Route("POST /test").Body(Input{}).HandlerFunc(managedBody)`, false},
		{"ordinary body mismatch", `api.Route("POST /test").Body(OtherInput{}).HandlerFunc(ordinaryBody)`, false},
		{"explicit rejects ordinary", `api.Route("GET /test").Response(200, Output{}).HandlerFunc(ordinary)`, false},
		{"explicit result mismatch", `api.Route("GET /test").Response(200, OtherOutput{}).HandlerFunc(managed)`, false},
		{"validator requires body", `api.Route("POST /test").Validator(check)`, false},
		{"response validator requires body", `api.Route("POST /test").Response(200, Output{}).Validator(check)`, false},
		{"validator body mismatch", `api.Route("POST /test").Body(Input{}).Validator(wrongCheck)`, false},
		{"managed validator body mismatch", `api.Route("POST /test").Body(Input{}).Response(200, Output{}).Validator(wrongCheck)`, false},
		{"validator requires error", `api.Route("POST /test").Body(Input{}).Validator(func(*http.Request, Input) {})`, false},
	}
	for _, chain := range []string{"Body(Input{})", "Response(200, Output{})"} {
		for _, annotation := range []string{"DescribeBody(Input{})", "DescribeQuery(Input{})", "DescribeParams(Input{})", "DescribeResponse(200, Output{})"} {
			fixtures = append(fixtures, compilationFixture{chain + "/" + annotation, `api.Route("POST /test").` + chain + `.` + annotation, false})
		}
	}
	fixtures = append(fixtures, compilationFixture{"described rejects managed handler", `api.Route("POST /test").DescribeBody(Input{}).HandlerFunc(managed)`, false})
	for _, chain := range []string{"Body(Input{}).Response(201, Output{})", "Response(201, Output{}).Body(Input{})"} {
		for _, handler := range []string{"wrongBody", "ordinaryBody", "managed"} {
			fixtures = append(fixtures, compilationFixture{chain + "/" + handler, `api.Route("POST /test").` + chain + `.HandlerFunc(` + handler + `)`, false})
		}
		if strings.Contains(chain, "Response") {
			fixtures = append(fixtures, compilationFixture{chain + "/wrongOutput", `api.Route("POST /test").` + chain + `.HandlerFunc(wrongOutput)`, false})
		}
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "fixture.go")
			if err := os.WriteFile(source, []byte(preamble+fixture.source+"\n}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", filepath.Join(dir, "fixture.a"), source)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("compiler timed out: %s", output)
			}
			if fixture.valid {
				if err != nil {
					t.Fatalf("valid fixture failed: %v\n%s", err, output)
				}
			} else if err == nil {
				t.Fatal("invalid handler compiled")
			} else if !strings.Contains(string(output), "HandlerFunc") && !strings.Contains(string(output), "Describe") && !strings.Contains(string(output), "Validator") {
				t.Fatalf("build failed for an unexpected reason: %v\n%s", err, output)
			}
		})
	}
}
