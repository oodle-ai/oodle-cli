package cmd

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNonASCIITitles(t *testing.T) {
	var dashboard map[string]interface{}
	err := json.Unmarshal([]byte(`{
		"title": "API – overview",
		"panels": [
			{"title": "Request rate", "type": "timeseries"},
			{"title": "CPU temperature (°C)", "type": "stat"},
			{"title": "Row", "type": "row", "panels": [
				{"title": "Checkout 🛒", "type": "stat"}
			]}
		]
	}`), &dashboard)
	if err != nil {
		t.Fatal(err)
	}
	got := nonASCIITitles(dashboard)
	want := []string{"API – overview", "CPU temperature (°C)", "Checkout 🛒"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nonASCIITitles() = %v, want %v", got, want)
	}
	if got := nonASCIITitles(nil); got != nil {
		t.Errorf("nonASCIITitles(nil) = %v, want nil", got)
	}
}
