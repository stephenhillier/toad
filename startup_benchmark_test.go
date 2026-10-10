package toad_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stephenhillier/toad"
)

// Each resource has distinct request, response, list, and detail models, with
// shared nested contact/address models like a production API. Generics keep the
// route setup consistent without reducing all endpoints to a single model type.
type startupInput[D any] struct {
	Name     string            `json:"name" validate:"required,min=2,max=100"`
	Slug     string            `json:"slug" validate:"required,alphanum,min=3,max=64"`
	Status   string            `json:"status" validate:"required,oneof=active archived draft"`
	Tags     []string          `json:"tags" validate:"omitempty,max=10,dive,required,max=32"`
	Metadata map[string]string `json:"metadata" validate:"omitempty,max=10,dive,keys,required,max=32,endkeys,max=256"`
	Owner    startupContact    `json:"owner" validate:"required"`
	Details  D                 `json:"details" validate:"required"`
}

type startupModel[D any] struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Slug     string            `json:"slug"`
	Status   string            `json:"status"`
	Tags     []string          `json:"tags"`
	Metadata map[string]string `json:"metadata"`
	Owner    startupContact    `json:"owner"`
	Details  D                 `json:"details"`
}

type startupList[D any] struct {
	Items      []startupModel[D] `json:"items"`
	Total      int               `json:"total"`
	NextCursor *string           `json:"next_cursor,omitempty"`
}

type startupContact struct {
	Name    string          `json:"name" validate:"required,min=2,max=100"`
	Email   string          `json:"email" validate:"required,email"`
	Address *startupAddress `json:"address,omitempty" validate:"omitempty"`
}

type startupAddress struct {
	Street  string `json:"street" validate:"required,max=200"`
	City    string `json:"city" validate:"required,max=100"`
	Country string `json:"country" validate:"required,len=2"`
}

type startupUser struct {
	Email string   `json:"email" validate:"required,email"`
	Roles []string `json:"roles" validate:"required,min=1,dive,oneof=admin member viewer"`
}

type startupTeam struct {
	Members []string `json:"members" validate:"required,min=1,max=100,dive,required"`
	Private bool     `json:"private"`
}

type startupProject struct {
	Budget float64  `json:"budget" validate:"gte=0,lte=1000000"`
	Teams  []string `json:"teams" validate:"required,min=1,dive,required"`
}

type startupTask struct {
	Priority int      `json:"priority" validate:"min=1,max=5"`
	Assignee *string  `json:"assignee,omitempty" validate:"omitempty,min=3"`
	Steps    []string `json:"steps" validate:"omitempty,max=20,dive,required"`
}

type startupProduct struct {
	SKU   string  `json:"sku" validate:"required,alphanum,max=32"`
	Price float64 `json:"price" validate:"gt=0,lte=100000"`
}

type startupCategory struct {
	ParentID *string `json:"parent_id,omitempty" validate:"omitempty,min=3"`
	Position int     `json:"position" validate:"gte=0"`
}

type startupOrder struct {
	Items    []startupLineItem `json:"items" validate:"required,min=1,max=100,dive"`
	Currency string            `json:"currency" validate:"required,oneof=USD CAD EUR"`
}

type startupLineItem struct {
	ProductID string `json:"product_id" validate:"required,min=3"`
	Quantity  int    `json:"quantity" validate:"min=1,max=1000"`
}

type startupInvoice struct {
	OrderID string  `json:"order_id" validate:"required,min=3"`
	Amount  float64 `json:"amount" validate:"gt=0"`
}

type startupPayment struct {
	InvoiceID string `json:"invoice_id" validate:"required,min=3"`
	Method    string `json:"method" validate:"required,oneof=card bank credit"`
}

type startupSubscription struct {
	PlanID string `json:"plan_id" validate:"required,min=3"`
	Seats  int    `json:"seats" validate:"min=1,max=10000"`
}

type startupCustomer struct {
	Billing startupAddress `json:"billing" validate:"required"`
	TaxID   *string        `json:"tax_id,omitempty" validate:"omitempty,max=32"`
}

type startupSupplier struct {
	Contact startupContact `json:"contact" validate:"required"`
	Website string         `json:"website" validate:"required,url"`
}

type startupWarehouse struct {
	Address  startupAddress `json:"address" validate:"required"`
	Capacity int            `json:"capacity" validate:"gt=0"`
}

type startupShipment struct {
	Destination startupAddress `json:"destination" validate:"required"`
	Weight      float64        `json:"weight" validate:"gt=0,lte=10000"`
}

type startupDocument struct {
	URL      string `json:"url" validate:"required,url"`
	MimeType string `json:"mime_type" validate:"required,max=100"`
	Size     int64  `json:"size" validate:"gte=0"`
}

type startupComment struct {
	Text       string   `json:"text" validate:"required,max=5000"`
	References []string `json:"references" validate:"omitempty,max=10,dive,url"`
}

type startupNotification struct {
	Channel    string   `json:"channel" validate:"required,oneof=email sms push"`
	Recipients []string `json:"recipients" validate:"required,min=1,max=100,dive,email"`
}

type startupWebhook struct {
	URL    string   `json:"url" validate:"required,url"`
	Events []string `json:"events" validate:"required,min=1,max=20,dive,required"`
}

type startupAPIKey struct {
	Scopes  []string `json:"scopes" validate:"required,min=1,max=20,dive,required"`
	TTLDays int      `json:"ttl_days" validate:"min=1,max=365"`
}

type startupAuditEvent struct {
	Action  string            `json:"action" validate:"required,max=100"`
	Changes map[string]string `json:"changes" validate:"required,min=1,dive,keys,required,endkeys,required"`
}

var (
	errStartupNotFound = errors.New("resource not found")
	errStartupConflict = errors.New("resource already exists")
)

var startupResources = []struct {
	name     string
	register func(*toad.Api, string)
}{
	{"users", registerStartupResource[startupUser]},
	{"teams", registerStartupResource[startupTeam]},
	{"projects", registerStartupResource[startupProject]},
	{"tasks", registerStartupResource[startupTask]},
	{"products", registerStartupResource[startupProduct]},
	{"categories", registerStartupResource[startupCategory]},
	{"orders", registerStartupResource[startupOrder]},
	{"invoices", registerStartupResource[startupInvoice]},
	{"payments", registerStartupResource[startupPayment]},
	{"subscriptions", registerStartupResource[startupSubscription]},
	{"customers", registerStartupResource[startupCustomer]},
	{"suppliers", registerStartupResource[startupSupplier]},
	{"warehouses", registerStartupResource[startupWarehouse]},
	{"shipments", registerStartupResource[startupShipment]},
	{"documents", registerStartupResource[startupDocument]},
	{"comments", registerStartupResource[startupComment]},
	{"notifications", registerStartupResource[startupNotification]},
	{"webhooks", registerStartupResource[startupWebhook]},
	{"api-keys", registerStartupResource[startupAPIKey]},
	{"audit-events", registerStartupResource[startupAuditEvent]},
}

func registerStartupResource[D any](api *toad.Api, path string) {
	api.Route("GET "+path).Title("List resources").Description("Return a paginated resource collection.").
		Response(http.StatusOK, startupList[D]{}).
		HandlerFunc(func(*http.Request) (startupList[D], error) { return startupList[D]{}, nil })
	api.Route("GET "+path+"/{id}").Title("Get resource").Description("Return a resource by ID.").
		Response(http.StatusOK, startupModel[D]{}).Error(http.StatusNotFound, errStartupNotFound).
		HandlerFunc(func(*http.Request) (startupModel[D], error) { return startupModel[D]{}, nil })
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		pattern, status := method+" "+path, http.StatusCreated
		if method == http.MethodPut {
			pattern, status = pattern+"/{id}", http.StatusOK
		}
		api.Route(pattern).Title("Write resource").Description("Validate and persist the resource.").
			Body(startupInput[D]{}).
			Validator(func(_ *http.Request, body startupInput[D]) error {
				if strings.TrimSpace(body.Name) == "" {
					return toad.Invalid("name", "Name must not be blank")
				}
				return nil
			}).
			Validator(func(_ *http.Request, body startupInput[D]) error {
				if body.Slug == "reserved" {
					return toad.Invalid("slug", "Slug is reserved")
				}
				return nil
			}).
			Response(status, startupModel[D]{}).
			Error(http.StatusNotFound, errStartupNotFound).Error(http.StatusConflict, errStartupConflict).
			HandlerFunc(func(_ *http.Request, body startupInput[D]) (startupModel[D], error) {
				return startupModel[D]{ID: "resource-1", Name: body.Name, Slug: body.Slug, Status: body.Status,
					Tags: body.Tags, Metadata: body.Metadata, Owner: body.Owner, Details: body.Details}, nil
			})
	}
	api.Route("DELETE "+path+"/{id}").Title("Delete resource").Description("Remove a resource by ID.").
		DescribeResponse(http.StatusNoContent, nil).
		HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

// Prepare the route names outside the timer. At 100 endpoints all 20 model
// families are present; larger cases reuse them under additional API versions,
// measuring route growth with realistic schema sharing rather than new types.
func startupRegistrations(endpoints int) []func(*toad.Api) {
	registrations := make([]func(*toad.Api), endpoints/5)
	for i := range registrations {
		resource := startupResources[i%len(startupResources)]
		path := fmt.Sprintf("/v%d/%s", 1+i/len(startupResources), resource.name)
		registrations[i] = func(api *toad.Api) { resource.register(api, path) }
	}
	return registrations
}

func newStartupAPI(registrations []func(*toad.Api)) (*toad.Api, *http.ServeMux) {
	mux := http.NewServeMux()
	api := toad.NewApi(mux).Title("Production API benchmark").Version("1.0.0").
		Description("CRUD resources with typed validation and shared nested models.").Server("/api")
	for _, register := range registrations {
		register(api)
	}
	return api, mux
}

// Every iteration uses a fresh API, ServeMux, and validator instance. Register
// includes metadata, handler adapters, error mappings, and validator callbacks.
// WithOpenAPI also reflects models/tags, validates the document, and serializes
// it, as an application that checks its contract before listening would do.
// Runtime validator caches remain lazy: no requests, listeners, process launch,
// application I/O, or prewarming are included in either measurement.
func BenchmarkAPIStartup(b *testing.B) {
	for _, endpoints := range []int{25, 100, 250} {
		b.Run(fmt.Sprintf("Endpoints%d", endpoints), func(b *testing.B) {
			registrations := startupRegistrations(endpoints)
			b.Run("Register", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					newStartupAPI(registrations)
				}
			})
			b.Run("WithOpenAPI", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					api, _ := newStartupAPI(registrations)
					if _, err := api.Generate(); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// Guard the workload: a missing route or unsupported model must not make the
// benchmark look faster. Check the generated contract and real validation path.
func TestStartupBenchmarkFixture(t *testing.T) {
	for _, endpoints := range []int{25, 100, 250} {
		t.Run(fmt.Sprintf("Endpoints%d", endpoints), func(t *testing.T) {
			api, mux := newStartupAPI(startupRegistrations(endpoints))
			data, err := api.Generate()
			if err != nil {
				t.Fatal(err)
			}
			doc, err := openapi3.NewLoader().LoadFromData(data)
			if err != nil {
				t.Fatal(err)
			}
			operations := 0
			for _, path := range doc.Paths.Map() {
				operations += len(path.Operations())
			}
			if operations != endpoints {
				t.Fatalf("document contains %d operations, want %d", operations, endpoints)
			}
			// Verify every body route reaches validation, including later versions.
			for i := range endpoints / 5 {
				resource := startupResources[i%len(startupResources)]
				path := fmt.Sprintf("/v%d/%s", 1+i/len(startupResources), resource.name)
				for _, method := range []string{http.MethodPost, http.MethodPut} {
					url := path
					if method == http.MethodPut {
						url += "/resource-1"
					}
					req := httptest.NewRequest(method, url, strings.NewReader(`{}`))
					req.Header.Set("Content-Type", "application/json")
					rr := httptest.NewRecorder()
					mux.ServeHTTP(rr, req)
					if rr.Code != http.StatusUnprocessableEntity {
						t.Fatalf("%s %s: %d %s", method, url, rr.Code, rr.Body)
					}
				}
			}
		})
	}

	_, mux := newStartupAPI(startupRegistrations(100))
	body := startupUserRequestBody
	for _, tc := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"valid", body, http.StatusCreated, `"id":"resource-1"`},
		{"nested tags", strings.ReplaceAll(body, "ada@example.com", "invalid"), http.StatusUnprocessableEntity, `"code":"email"`},
		{"collection tags", strings.Replace(body, `"admin"`, `"unknown"`, 1), http.StatusUnprocessableEntity, `"code":"oneof"`},
		{"first validator", strings.Replace(body, `"name":"Ada"`, `"name":"   "`, 1), http.StatusUnprocessableEntity, `"message":"Name must not be blank"`},
		{"second validator", strings.Replace(body, `"slug":"ada"`, `"slug":"reserved"`, 1), http.StatusUnprocessableEntity, `"message":"Slug is reserved"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/users", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != tc.status || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("response: %d %s; want %d containing %s", rr.Code, rr.Body, tc.status, tc.want)
			}
		})
	}
}
