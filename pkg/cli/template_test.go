package cli

import (
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func TestMonthlyCost(t *testing.T) {
	prices := []*client.Price{
		{ResourceType: "compute.cpu", Currency: "EUR", UnitPrice: "0.012"},
		{ResourceType: "compute.memory", Currency: "EUR", UnitPrice: "0.0025"},
		{ResourceType: "database.storage", Currency: "EUR", UnitPrice: "0.00005"},
	}

	cost, ok := monthlyCost(map[string]string{"compute.cpu": "0.25", "compute.memory": "0.5", "database.storage": "20"}, prices)
	if !ok || cost != "3.83 EUR" {
		t.Fatalf("priced reservation: got %q, %v; want 3.83 EUR", cost, ok)
	}

	if cost, ok := monthlyCost(map[string]string{"compute.cpu": "0.25", "database.cpu": "1.5"}, prices); ok {
		t.Fatalf("a reserved type with no rate must be no answer, not free: got %q", cost)
	}

	if cost, ok := monthlyCost(nil, prices); ok {
		t.Fatalf("no reservation is no answer, not a zero cost: got %q", cost)
	}
}
