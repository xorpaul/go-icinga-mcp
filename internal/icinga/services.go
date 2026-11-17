package icinga

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xorpaul/go-icinga-mcp/internal/config"
)

type Service struct {
	cfg *config.Settings
	c   *Client
}

func NewService(c *Client, cfg *config.Settings) *Service {
	return &Service{cfg: cfg, c: c}
}

// ListOpts is the per-call shape for listing/filter requests against the
// Icinga 2 REST API.
//
//   - Filter is the user-supplied raw Icinga DSL expression (may be empty).
//   - Extras are caller-built DSL clauses to AND with Filter.
//   - Page/Limit are applied client-side after the response is unwrapped,
//     because /v1/objects/<type> does not paginate server-side.
type ListOpts struct {
	Page   *int
	Limit  *int
	Filter string
	Extras []string
}

func (s *Service) listEndpoint(ctx context.Context, key string, opts ListOpts) (any, error) {
	ep, err := s.cfg.Endpoint(key)
	if err != nil {
		return nil, err
	}
	params := map[string]string{}
	if filter := composeFilter(opts.Filter, opts.Extras); filter != "" {
		params["filter"] = filter
	}
	raw, err := s.c.GetJSON(ctx, ep, params)
	if err != nil {
		return nil, err
	}
	list := UnwrapResults(raw)
	return paginate(list, opts.Page, opts.Limit), nil
}

// composeFilter joins a user-supplied DSL filter with any caller-built clauses
// using `&&`, parenthesising each piece so mixed operators don't change
// precedence in surprising ways.
func composeFilter(user string, extras []string) string {
	parts := make([]string, 0, len(extras)+1)
	if u := strings.TrimSpace(user); u != "" {
		parts = append(parts, "("+u+")")
	}
	for _, e := range extras {
		if t := strings.TrimSpace(e); t != "" {
			parts = append(parts, "("+t+")")
		}
	}
	return strings.Join(parts, " && ")
}

// paginate slices a result list using 1-indexed Page and Limit. A nil Limit
// returns the list as-is. Out-of-range pages return an empty slice.
func paginate(list []any, page, limit *int) []any {
	if limit == nil {
		return list
	}
	lim := *limit
	if lim < 0 {
		lim = 0
	}
	p := 1
	if page != nil && *page > 1 {
		p = *page
	}
	start := (p - 1) * lim
	if start >= len(list) {
		return []any{}
	}
	end := start + lim
	if end > len(list) {
		end = len(list)
	}
	return list[start:end]
}

func (s *Service) ListHosts(ctx context.Context, o ListOpts) (any, error) {
	return s.listEndpoint(ctx, "hosts", o)
}

func (s *Service) ListServices(ctx context.Context, o ListOpts) (any, error) {
	return s.listEndpoint(ctx, "services", o)
}

func (s *Service) ListDowntimes(ctx context.Context, o ListOpts) (any, error) {
	return s.listEndpoint(ctx, "downtimes", o)
}

func (s *Service) ListComments(ctx context.Context, o ListOpts) (any, error) {
	return s.listEndpoint(ctx, "comments", o)
}

func (s *Service) ListHostGroups(ctx context.Context, o ListOpts) (any, error) {
	return s.listEndpoint(ctx, "hostgroups", o)
}

func (s *Service) ListServiceGroups(ctx context.Context, o ListOpts) (any, error) {
	return s.listEndpoint(ctx, "servicegroups", o)
}

// DowntimeOpts is the per-call shape for the schedule-downtime action.
//
// Either EndTime or DurationSec must be set. For flexible (non-fixed)
// downtimes DurationSec is also required even if EndTime is given, since
// Icinga 2 uses `duration` to size the downtime window once it triggers.
type DowntimeOpts struct {
	Host        string
	Service     string
	Author      string
	Comment     string
	StartTime   *int64
	EndTime     *int64
	DurationSec *int64
	Fixed       *bool
	AllServices *bool
	TriggerName string
}

func (s *Service) ScheduleDowntime(ctx context.Context, o DowntimeOpts) (any, error) {
	if o.Host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if strings.TrimSpace(o.Comment) == "" {
		return nil, fmt.Errorf("comment is required")
	}

	objType := "Host"
	filter := fmt.Sprintf("host.name == %q", o.Host)
	if o.Service != "" {
		objType = "Service"
		filter = fmt.Sprintf("host.name == %q && service.name == %q", o.Host, o.Service)
	}

	start := time.Now().Unix()
	if o.StartTime != nil {
		start = *o.StartTime
	}
	var end int64
	switch {
	case o.EndTime != nil:
		end = *o.EndTime
	case o.DurationSec != nil:
		end = start + *o.DurationSec
	default:
		return nil, fmt.Errorf("end_time or duration_seconds is required")
	}
	if end <= start {
		return nil, fmt.Errorf("end_time (%d) must be after start_time (%d)", end, start)
	}

	fixed := true
	if o.Fixed != nil {
		fixed = *o.Fixed
	}

	author := o.Author
	if author == "" {
		author = s.cfg.Username
	}
	if author == "" {
		return nil, fmt.Errorf("author is required (no configured username to fall back on)")
	}

	body := map[string]any{
		"type":       objType,
		"filter":     filter,
		"author":     author,
		"comment":    o.Comment,
		"start_time": start,
		"end_time":   end,
		"fixed":      fixed,
	}
	if !fixed {
		if o.DurationSec == nil {
			return nil, fmt.Errorf("duration_seconds is required for flexible downtimes")
		}
		body["duration"] = *o.DurationSec
	}
	if o.Service == "" && o.AllServices != nil && *o.AllServices {
		body["all_services"] = true
	}
	if o.TriggerName != "" {
		body["trigger_name"] = o.TriggerName
	}

	ep, err := s.cfg.Endpoint("schedule_downtime")
	if err != nil {
		return nil, err
	}
	return s.c.PostJSON(ctx, ep, body)
}
