package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func Test_app_readIDParam(t *testing.T) {
	tests := []struct {
		name        string
		paramId     string
		expectError bool
	}{
		{"valid id", uuid.NewString(), false},
		{"invalid id", "", true},
		{"invalid id", "test", true},
	}

	for _, test := range tests {
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		chiCtx := chi.NewRouteContext()
		chiCtx.URLParams.Add("id", test.paramId)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
		_, err := app.readIDParam(req, "id")
		if !test.expectError && err != nil {
			t.Errorf("%s: expected no err, but got one: %q", test.name, err)
		}
		if test.expectError && err == nil {
			t.Errorf("%s: expected err, but didn't get one", test.name)
		}
	}
}

func Test_app_writeJSON(t *testing.T) {
	tests := []struct {
		name        string
		data        any
		expectError bool
	}{
		{"valid data", map[string]string{"test": "yes"}, false},
		{"invalid data", func() {}, true},
	}

	for _, test := range tests {
		rr := httptest.NewRecorder()
		err := app.writeJSON(rr, http.StatusOK, envelope{"data": test.data}, nil)
		if !test.expectError && err != nil {
			t.Errorf("%s: expected no err, but got one: %q", test.name, err)
		}
		if test.expectError && err == nil {
			t.Errorf("%s: expected err, but didn't get one", test.name)
		}
	}
}

func Test_app_readJSON(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		expectError bool
		err         string
	}{
		{"valid json", `{"test":"yes"}`, false, ""},
		{"wrong type of field err", `{"test":3}`, true, "body contains incorrect JSON type for field"},
		{"badly-formed json", `{"test":,}`, true, "body contains badly-formed JSON"},
		{"badly-formed json", `<?>`, true, "body contains badly-formed JSON (at character 1)"},
		{"wrong type", `["test"]`, true, "body contains incorrect JSON type (at character 1)"},
		{"empty body", "", true, "body must not be empty"},
		{"unknown field in body", `{"pizza":true}`, true, "body contains unknown key"},
		{"extra JSON", `{"test":"yes"}{"pizza":false}`, true, "body must only contain a single JSON value"},
		{"JSON file to large", getLargeJSONString(t), true, "body must not be larger than"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := strings.NewReader(test.json)
			req, _ := http.NewRequest(http.MethodGet, "/", reader)
			var dst struct {
				Test string `json:"test"`
			}
			rr := httptest.NewRecorder()

			err := app.readJSON(rr, req, &dst)
			if !test.expectError && err != nil {
				t.Errorf("expected no err, but got one: %q", err)
			}
			if test.expectError && err == nil {
				t.Error("expected err, but didn't get one")
			}
			if test.expectError && err != nil && !strings.Contains(err.Error(), test.err) {
				t.Errorf("error does not cointain expected string %q", test.err)
			}
		})
	}
}

func getLargeJSONString(t *testing.T) string {
	t.Helper()
	largeJSONPath := "./testdata/large.json"
	file, err := os.Open(largeJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var js struct {
		Test string `json:"test"`
	}
	byteValue, _ := io.ReadAll(file)
	err = json.Unmarshal(byteValue, &js)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%q", js)
}
