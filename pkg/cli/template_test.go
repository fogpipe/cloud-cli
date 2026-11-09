package cli

import (
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

func TestTemplateCost(t *testing.T) {
	prices := []*client.Price{
		{ResourceType: "compute.cpu", Currency: "EUR", UnitPrice: "0.012"},
		{ResourceType: "compute.memory", Currency: "EUR", UnitPrice: "0.0025"},
		{ResourceType: "database.storage", Currency: "EUR", UnitPrice: "0.00005"},
	}
	held := map[string]string{"compute.cpu": "0.25", "compute.memory": "0.5", "database.storage": "20"}

	cost, ok := templateCost(client.Template{Reserved: held, Floor: held}, prices)
	if !ok || cost != "3.83 EUR" {
		t.Fatalf("a floor at the reservation: got %q, %v; want 3.83 EUR", cost, ok)
	}

	floor := map[string]string{"compute.cpu": "0.025", "compute.memory": "0.5", "database.storage": "20"}
	cost, ok = templateCost(client.Template{Reserved: held, Floor: floor}, prices)
	if !ok || cost != "1.86 – 3.83 EUR" {
		t.Fatalf("CPU billed from a tenth: got %q, %v; want 1.86 – 3.83 EUR", cost, ok)
	}

	if cost, ok := templateCost(client.Template{Reserved: map[string]string{"compute.cpu": "0.25", "database.cpu": "1.5"}}, prices); ok {
		t.Fatalf("a reserved type with no rate must be no answer, not free: got %q", cost)
	}

	if cost, ok := templateCost(client.Template{}, prices); ok {
		t.Fatalf("no reservation is no answer, not a zero cost: got %q", cost)
	}
}
