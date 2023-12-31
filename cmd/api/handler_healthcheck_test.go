package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func Test_App_healthcheckHandler(t *testing.T) {
	expectedStatus := http.StatusOK
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	handler := http.HandlerFunc(app.healthcheckHandler)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != expectedStatus {
		t.Errorf("expected status code %d, but got %d", expectedStatus, rr.Code)
	}
}
