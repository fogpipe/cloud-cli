package cli

import (
	"strings"
	"testing"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// A degraded database says why on the read that says it is degraded.
func TestDBGetListsADegradedDatabasesProblem(t *testing.T) {
	rows := dbDetailRows(&client.Database{
		Name:   "stats-db",
		Status: "degraded",
		Problems: []client.StatusProblem{{
			Reason: "NotManaged",
			Detail: "The platform is not managing this database.",
		}},
	}, "host:5432")

	var status, problem int = -1, -1
	for i, row := range rows {
		switch row[0] {
		case "Status":
			status = i
		case "Problem":
			problem = i
			if !strings.Contains(row[1], "NotManaged — The platform is not managing this database.") {
				t.Errorf("problem row = %q", row[1])
			}
		}
	}
	if status < 0 || problem != status+1 {
		t.Errorf("the problem belongs directly under the status: status at %d, problem at %d", status, problem)
	}

	for _, row := range dbDetailRows(&client.Database{Name: "main", Status: "running"}, "host:5432") {
		if row[0] == "Problem" {
			t.Errorf("a healthy database lists no problem, got %q", row[1])
		}
	}
}
