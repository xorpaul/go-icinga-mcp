package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xorpaul/go-icinga-mcp/internal/config"
	"github.com/xorpaul/go-icinga-mcp/internal/icinga"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ServerName = "go-icinga-mcp"
const ServerVersion = "0.4.1"

func New(cfg *config.Settings, svc *icinga.Service) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: ServerVersion}, nil)
	register(srv, cfg, svc)
	return srv
}

// listingArgs is the shared input shape for the host/service/group/comment/downtime list tools.
type listingArgs struct {
	FilterExpr string `json:"filter_expr,omitempty" jsonschema:"Icinga 2 DSL filter expression (e.g. host.name == \"web01\" || \"linux\" in host.groups)"`
	Page       *int   `json:"page,omitempty" jsonschema:"1-indexed page number, applied client-side"`
	Limit      *int   `json:"limit,omitempty" jsonschema:"Page size, applied client-side"`
	Fields     string `json:"fields,omitempty" jsonschema:"Comma-separated dotted paths overriding the summary projection"`
	Summary    *bool  `json:"summary,omitempty" jsonschema:"When true (default), apply the summary projection; false returns full upstream objects"`
}

type detailArgs struct {
	Name    string `json:"name" jsonschema:"Exact host name"`
	Fields  string `json:"fields,omitempty"`
	Summary *bool  `json:"summary,omitempty" jsonschema:"When true, apply summary projection (defaults to false for detail tools)"`
}

type serviceDetailArgs struct {
	Name    string `json:"name" jsonschema:"Exact service name"`
	Host    string `json:"host" jsonschema:"Host the service is attached to"`
	Fields  string `json:"fields,omitempty"`
	Summary *bool  `json:"summary,omitempty"`
}

type problemListArgs struct {
	Host        string `json:"host,omitempty" jsonschema:"Filter by host name; * and ? wildcards are supported (e.g. \"*.example.com\")"`
	ServiceName string `json:"service_name,omitempty" jsonschema:"Filter by service name; * and ? wildcards are supported"`
	Page        *int   `json:"page,omitempty"`
	Limit       *int   `json:"limit,omitempty"`
	Fields      string `json:"fields,omitempty"`
	Summary     *bool  `json:"summary,omitempty"`
}

type groupListArgs struct {
	Name   string `json:"name,omitempty" jsonschema:"Exact group name; when set, filters the list to that single group"`
	Page   *int   `json:"page,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
	Fields string `json:"fields,omitempty"`
}

type scheduleDowntimeArgs struct {
	Host        string `json:"host" jsonschema:"Host name (required)"`
	Service     string `json:"service,omitempty" jsonschema:"Service name; omit for a host-level downtime"`
	Author      string `json:"author,omitempty" jsonschema:"Downtime author; defaults to the configured API username"`
	Comment     string `json:"comment" jsonschema:"Downtime comment (required)"`
	StartTime   *int64 `json:"start_time,omitempty" jsonschema:"Unix timestamp; defaults to now"`
	EndTime     *int64 `json:"end_time,omitempty" jsonschema:"Unix timestamp; mutually exclusive with duration_seconds"`
	DurationSec *int64 `json:"duration_seconds,omitempty" jsonschema:"Length in seconds (used to derive end_time when not given). Required for flexible downtimes."`
	Fixed       *bool  `json:"fixed,omitempty" jsonschema:"Fixed (true) or flexible (false). Defaults true."`
	AllServices *bool  `json:"all_services,omitempty" jsonschema:"For host-level downtimes, also schedule downtime on every service on the host"`
	TriggerName string `json:"trigger_name,omitempty" jsonschema:"Optional name of an existing downtime that triggers this one"`
}

func register(srv *mcp.Server, cfg *config.Settings, svc *icinga.Service) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_hosts",
		Description: "List Icinga 2 host objects. Summarized by default to keep payloads small.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listingArgs) (*mcp.CallToolResult, any, error) {
		data, err := svc.ListHosts(ctx, icinga.ListOpts{Page: in.Page, Limit: in.Limit, Filter: in.FilterExpr})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(maybeProject(data, in.Fields, cfg.HostSummaryFields, in.Summary)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_services",
		Description: "List Icinga 2 service objects. Summarized by default.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listingArgs) (*mcp.CallToolResult, any, error) {
		data, err := svc.ListServices(ctx, icinga.ListOpts{Page: in.Page, Limit: in.Limit, Filter: in.FilterExpr})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(maybeProject(data, in.Fields, cfg.ServiceSummaryFields, in.Summary)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_host_problems",
		Description: "List hosts currently in a hard problem state (state != 0 && state_type == HARD).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in problemListArgs) (*mcp.CallToolResult, any, error) {
		extras := []string{"host.state != 0", "host.state_type == 1"}
		if in.Host != "" {
			extras = append(extras, nameFilter("host.name", in.Host))
		}
		data, err := svc.ListHosts(ctx, icinga.ListOpts{Page: in.Page, Limit: in.Limit, Extras: extras})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(maybeProject(data, in.Fields, cfg.HostSummaryFields, in.Summary)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_service_problems",
		Description: "List services currently in a hard problem state. Filter by host and/or service.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in problemListArgs) (*mcp.CallToolResult, any, error) {
		extras := []string{"service.state != 0", "service.state_type == 1"}
		if in.Host != "" {
			extras = append(extras, nameFilter("host.name", in.Host))
		}
		if in.ServiceName != "" {
			extras = append(extras, nameFilter("service.name", in.ServiceName))
		}
		data, err := svc.ListServices(ctx, icinga.ListOpts{Page: in.Page, Limit: in.Limit, Extras: extras})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(maybeProject(data, in.Fields, cfg.ServiceSummaryFields, in.Summary)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_host_detail",
		Description: "Fetch a single host by exact name. Full object by default.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in detailArgs) (*mcp.CallToolResult, any, error) {
		extras := []string{fmt.Sprintf("host.name == %q", in.Name)}
		data, err := svc.ListHosts(ctx, icinga.ListOpts{Extras: extras})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(applyProjection(data, in.Fields, cfg.HostSummaryFields, derefBool(in.Summary, false))), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_service_detail",
		Description: "Fetch a single service by exact name and host. Full object by default.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in serviceDetailArgs) (*mcp.CallToolResult, any, error) {
		extras := []string{
			fmt.Sprintf("host.name == %q", in.Host),
			fmt.Sprintf("service.name == %q", in.Name),
		}
		data, err := svc.ListServices(ctx, icinga.ListOpts{Extras: extras})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(applyProjection(data, in.Fields, cfg.ServiceSummaryFields, derefBool(in.Summary, false))), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_hostgroups",
		Description: "List host groups; pass 'name' to narrow to a single group.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in groupListArgs) (*mcp.CallToolResult, any, error) {
		extras := []string{}
		if in.Name != "" {
			extras = append(extras, fmt.Sprintf("name == %q", in.Name))
		}
		data, err := svc.ListHostGroups(ctx, icinga.ListOpts{Page: in.Page, Limit: in.Limit, Extras: extras})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(data), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_servicegroups",
		Description: "List service groups; pass 'name' to narrow to a single group.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in groupListArgs) (*mcp.CallToolResult, any, error) {
		extras := []string{}
		if in.Name != "" {
			extras = append(extras, fmt.Sprintf("name == %q", in.Name))
		}
		data, err := svc.ListServiceGroups(ctx, icinga.ListOpts{Page: in.Page, Limit: in.Limit, Extras: extras})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(data), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_downtimes",
		Description: "List active and scheduled downtimes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listingArgs) (*mcp.CallToolResult, any, error) {
		data, err := svc.ListDowntimes(ctx, icinga.ListOpts{Page: in.Page, Limit: in.Limit, Filter: in.FilterExpr})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(maybeProject(data, in.Fields, cfg.DowntimeSummaryFields, in.Summary)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_comments",
		Description: "List comments on hosts and services.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listingArgs) (*mcp.CallToolResult, any, error) {
		data, err := svc.ListComments(ctx, icinga.ListOpts{Page: in.Page, Limit: in.Limit, Filter: in.FilterExpr})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(maybeProject(data, in.Fields, cfg.CommentSummaryFields, in.Summary)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "schedule_downtime",
		Description: "Schedule a downtime on a host or service via Icinga 2's schedule-downtime action. Provide either end_time or duration_seconds.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in scheduleDowntimeArgs) (*mcp.CallToolResult, any, error) {
		data, err := svc.ScheduleDowntime(ctx, icinga.DowntimeOpts{
			Host:        in.Host,
			Service:     in.Service,
			Author:      in.Author,
			Comment:     in.Comment,
			StartTime:   in.StartTime,
			EndTime:     in.EndTime,
			DurationSec: in.DurationSec,
			Fixed:       in.Fixed,
			AllServices: in.AllServices,
			TriggerName: in.TriggerName,
		})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(data), nil, nil
	})
}

// nameFilter returns an Icinga DSL clause that matches `field` against `name`.
// When `name` contains * or ? wildcards it uses match(); otherwise exact ==.
func nameFilter(field, name string) string {
	if strings.ContainsAny(name, "*?") {
		return fmt.Sprintf("match(%q, %s)", name, field)
	}
	return fmt.Sprintf("%s == %q", field, name)
}

// maybeProject applies the summary projection unless `summary` is explicitly
// false. The default is true (matches the Python behavior).
func maybeProject(data any, csvFields string, defaults []string, summary *bool) any {
	if summary != nil && !*summary {
		return data
	}
	return applyProjection(data, csvFields, defaults, true)
}

func applyProjection(data any, csvFields string, defaults []string, summary bool) any {
	if !summary {
		return data
	}
	fields := icinga.ParseFields(csvFields, defaults)
	if list, ok := data.([]any); ok && len(list) > 0 {
		return icinga.ProjectList(list, fields)
	}
	return data
}

func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// jsonResult builds a CallToolResult containing the JSON-serialized payload as
// a single TextContent block. Handlers return this alongside `nil` for the
// typed output so the SDK skips the structuredContent path — the MCP spec
// requires structuredContent to be a JSON object, but our payloads are
// frequently arrays (lists of hosts, services, etc.).
func jsonResult(data any) *mcp.CallToolResult {
	b, err := json.Marshal(data)
	if err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("marshal error: %v", err)}},
		}
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}
}
