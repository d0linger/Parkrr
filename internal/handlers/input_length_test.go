package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInputLengthPolicy(t *testing.T) {
	h := &Handler{}

	t.Run("CreatePerson", func(t *testing.T) {
		longName := strings.Repeat("a", 101)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/persons",
			strings.NewReader(`{"first_name":"`+longName+`","last_name":"Doe"}`))
		h.CreatePerson(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for long first_name, got %d", rec.Code)
		}

		longEmail := strings.Repeat("a", 256)
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/persons",
			strings.NewReader(`{"first_name":"John","last_name":"Doe","email":"`+longEmail+`"}`))
		h.CreatePerson(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for long email, got %d", rec.Code)
		}

		longNote := strings.Repeat("a", 2001)
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/persons",
			strings.NewReader(`{"first_name":"John","last_name":"Doe","notes":"`+longNote+`"}`))
		h.CreatePerson(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for long notes, got %d", rec.Code)
		}
	})

	t.Run("CreateVehicle", func(t *testing.T) {
		longLabel := strings.Repeat("a", 101)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/vehicles",
			strings.NewReader(`{"person_id":1,"category_id":1,"label":"`+longLabel+`","start_date":"2024-01-01"}`))
		h.CreateVehicle(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for long label, got %d", rec.Code)
		}
	})
}
