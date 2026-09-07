package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fogpipe/cloud-cli/pkg/client"
)

// runnerServicesFromFlags builds the pool's service set from --service and its
// three per-service modifiers (fogpipe/cloud-workspace#306).
//
// The modifiers name the service they belong to, because a pool may carry more
// than one and a bare --service-env would have to guess which:
//
//	--service postgres=postgres:18-alpine
//	--service-env postgres=POSTGRES_PASSWORD=hunter2
//	--service-memory postgres=1Gi
//
// A modifier naming a service that no --service declared is an error rather
// than a value that goes nowhere — that is the whole failure mode of a flag
// pair keyed by a string.
//
// Returns nil when nothing was named, so create sends no services and update
// leaves the pool's alone.
func runnerServicesFromFlags(cmd *cobra.Command) ([]client.RunnerService, error) {
	decls, err := cmd.Flags().GetStringArray("service")
	if err != nil {
		return nil, err
	}
	envs, err := cmd.Flags().GetStringArray("service-env")
	if err != nil {
		return nil, err
	}
	cpus, err := cmd.Flags().GetStringArray("service-cpu")
	if err != nil {
		return nil, err
	}
	mems, err := cmd.Flags().GetStringArray("service-memory")
	if err != nil {
		return nil, err
	}
	if len(decls)+len(envs)+len(cpus)+len(mems) == 0 {
		return nil, nil
	}

	order := make([]string, 0, len(decls))
	byName := map[string]*client.RunnerService{}
	for _, d := range decls {
		name, image, ok := strings.Cut(d, "=")
		if !ok || name == "" || image == "" {
			return nil, fmt.Errorf("--service %q: expected <name>=<image>, e.g. --service postgres=postgres:18-alpine", d)
		}
		if _, dup := byName[name]; dup {
			return nil, fmt.Errorf("--service %q: %q is declared twice", d, name)
		}
		byName[name] = &client.RunnerService{Name: name, Image: image}
		order = append(order, name)
	}

	find := func(flag, spec string) (*client.RunnerService, string, error) {
		name, rest, ok := strings.Cut(spec, "=")
		if !ok || name == "" {
			return nil, "", fmt.Errorf("--%s %q: expected <service>=…", flag, spec)
		}
		svc, known := byName[name]
		if !known {
			return nil, "", fmt.Errorf("--%s %q names service %q, which no --service declares%s", flag, spec, name, declaredList(order))
		}
		return svc, rest, nil
	}

	for _, e := range envs {
		svc, rest, err := find("service-env", e)
		if err != nil {
			return nil, err
		}
		key, value, ok := strings.Cut(rest, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("--service-env %q: expected <service>=<KEY>=<VALUE>", e)
		}
		if svc.Env == nil {
			svc.Env = map[string]string{}
		}
		svc.Env[key] = value
	}
	for _, c := range cpus {
		svc, rest, err := find("service-cpu", c)
		if err != nil {
			return nil, err
		}
		svc.CPU = rest
	}
	for _, m := range mems {
		svc, rest, err := find("service-memory", m)
		if err != nil {
			return nil, err
		}
		svc.Memory = rest
	}

	out := make([]client.RunnerService, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

func declaredList(order []string) string {
	if len(order) == 0 {
		return " (none were declared — add --service " + "<name>=<image>)"
	}
	sorted := append([]string(nil), order...)
	sort.Strings(sorted)
	return " (declared: " + strings.Join(sorted, ", ") + ")"
}

// runnerServiceFlags are shared by create and update.
func runnerServiceFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("service", nil, "Container to run beside every job, as <name>=<image> (repeatable), e.g. postgres=postgres:18-alpine")
	cmd.Flags().StringArray("service-env", nil, "Environment for a service, as <service>=<KEY>=<VALUE> (repeatable)")
	cmd.Flags().StringArray("service-cpu", nil, "CPU limit for a service, as <service>=<value> (repeatable)")
	cmd.Flags().StringArray("service-memory", nil, "Memory limit for a service, as <service>=<value> (repeatable)")
}

// serviceCell renders a pool's services for a table or an info box.
func serviceCell(services []client.RunnerService) string {
	if len(services) == 0 {
		return "—"
	}
	out := make([]string, len(services))
	for i, s := range services {
		out[i] = s.Name + " (" + s.Image + ")"
	}
	return strings.Join(out, ", ")
}
