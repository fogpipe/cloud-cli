package client

import (
	"fmt"
	"net/url"
	"reflect"
	"strconv"
)

func encodeQuery(q any) string {
	v := reflect.ValueOf(q)
	values := url.Values{}
	for i := range v.NumField() {
		name := v.Type().Field(i).Tag.Get("query")
		f := v.Field(i)
		if name == "" || f.IsZero() {
			continue
		}
		switch f.Kind() {
		case reflect.Slice:
			values[name] = f.Interface().([]string)
		case reflect.Pointer:
			values.Set(name, formatScalar(f.Elem()))
		default:
			values.Set(name, formatScalar(f))
		}
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

func formatScalar(f reflect.Value) string {
	switch f.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(f.Bool())
	case reflect.Int:
		return strconv.FormatInt(f.Int(), 10)
	}
	return f.String()
}

func DecodeQuery(values url.Values, q any) error {
	v := reflect.ValueOf(q).Elem()
	for i := range v.NumField() {
		name := v.Type().Field(i).Tag.Get("query")
		if name == "" || !values.Has(name) {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Slice:
			f.Set(reflect.ValueOf(values[name]))
		case reflect.Pointer:
			elem := reflect.New(f.Type().Elem())
			if err := parseScalar(elem.Elem(), name, values.Get(name)); err != nil {
				return err
			}
			f.Set(elem)
		default:
			if err := parseScalar(f, name, values.Get(name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseScalar(f reflect.Value, name, raw string) error {
	switch f.Kind() {
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%s: %q is not true or false", name, raw)
		}
		f.SetBool(b)
	case reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("%s: %q is not a whole number", name, raw)
		}
		f.SetInt(int64(n))
	default:
		f.SetString(raw)
	}
	return nil
}

type AuditQuery struct {
	ResourceType string `query:"resource_type"`
	ResourceID   string `query:"resource_id"`
	Actor        string `query:"actor"`
	Limit        int    `query:"limit"`
	Offset       int    `query:"offset"`
}

type CostQuery struct {
	From     string `query:"from"`
	To       string `query:"to"`
	GroupBy  string `query:"group_by"`
	Project  string `query:"project"`
	Resource string `query:"resource"`
}

type InvoiceUsageQuery struct {
	GroupBy string `query:"group_by"`
}

type CapacityQuery struct {
	From string `query:"from"`
	To   string `query:"to"`
	Step string `query:"step"`
}

type ChangelogQuery struct {
	Limit  int    `query:"limit"`
	Before string `query:"before"`
}

type DatabaseConnectionQuery struct {
	ReadOnly bool `query:"read_only"`
}

type DatabaseTunnelQuery struct {
	Replica bool `query:"replica"`
}

type DatabaseAuditQuery struct {
	Since string `query:"since"`
	Until string `query:"until"`
	Limit int    `query:"limit"`
}

type ExecQuery struct {
	Cmd       []string `query:"cmd"`
	Container string   `query:"container"`
	TTY       bool     `query:"tty"`
}

type BucketLifecycleRuleQuery struct {
	Prefix *string `query:"prefix"`
	All    bool    `query:"all"`
}

type BucketObjectsQuery struct {
	Prefix    string `query:"prefix"`
	Delimiter string `query:"delimiter"`
}

type BucketObjectQuery struct {
	Key string `query:"key"`
}

type RepositoryQuery struct {
	Repo string `query:"repo"`
}

type RepositoryTagQuery struct {
	Repo string `query:"repo"`
	Tag  string `query:"tag"`
}

type RepositoryScanQuery struct {
	Repo string `query:"repo"`
	Ref  string `query:"ref"`
}

type JobLogsQuery struct {
	Run string `query:"run"`
}

type GitHubConnectQuery struct {
	Account string `query:"account"`
}
