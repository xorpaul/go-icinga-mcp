package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Settings struct {
	BaseURL  string
	Username string
	Password string

	VerifyTLS  bool
	CABundle   string
	ClientCert string
	ClientKey  string

	RequestTimeoutSec float64
	MaxRetries        int
	RetryBackoffSec   float64

	HostSummaryFields     []string
	ServiceSummaryFields  []string
	CommentSummaryFields  []string
	DowntimeSummaryFields []string

	Endpoints map[string]string
}

// fileSchema is the on-disk YAML shape. Keep it separate from Settings so the
// public type stays free of yaml tags and stays stable for callers.
//
// The top-level key is `icinga2` — the v0.2 release retargeted the binary at
// the Icinga 2 REST API on port 5665. Older configs using `icinga_web` are
// still accepted as a fallback so existing installs keep working until they're
// updated.
type fileSchema struct {
	Icinga2 struct {
		BaseURL    string `yaml:"base_url"`
		Username   string `yaml:"username"`
		Password   string `yaml:"password"`
		VerifyTLS  *bool  `yaml:"verify_tls"`
		CABundle   string `yaml:"ca_bundle"`
		ClientCert string `yaml:"client_cert"`
		ClientKey  string `yaml:"client_key"`
	} `yaml:"icinga2"`

	IcingaWeb struct {
		BaseURL    string `yaml:"base_url"`
		Username   string `yaml:"username"`
		Password   string `yaml:"password"`
		VerifyTLS  *bool  `yaml:"verify_tls"`
		CABundle   string `yaml:"ca_bundle"`
		ClientCert string `yaml:"client_cert"`
		ClientKey  string `yaml:"client_key"`
	} `yaml:"icinga_web"`

	HTTP struct {
		RequestTimeoutSec *float64 `yaml:"request_timeout_sec"`
		MaxRetries        *int     `yaml:"max_retries"`
		RetryBackoffSec   *float64 `yaml:"retry_backoff_sec"`
	} `yaml:"http"`

	SummaryFields struct {
		Host     []string `yaml:"host"`
		Service  []string `yaml:"service"`
		Comment  []string `yaml:"comment"`
		Downtime []string `yaml:"downtime"`
	} `yaml:"summary_fields"`
}

// DefaultConfigPath returns <dir-of-running-binary>/config.yaml. Used when the
// caller did not pass an explicit -config path.
func DefaultConfigPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = resolved
	}
	return filepath.Join(filepath.Dir(exe), "config.yaml"), nil
}

// Load reads the YAML config at path. If path is empty, DefaultConfigPath() is
// used. A missing default file is allowed (defaults apply); a missing explicit
// path is an error.
func Load(path string) (*Settings, error) {
	explicit := path != ""
	if !explicit {
		p, err := DefaultConfigPath()
		if err != nil {
			return nil, err
		}
		path = p
	}

	var f fileSchema
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if uerr := yaml.Unmarshal(data, &f); uerr != nil {
			return nil, fmt.Errorf("parse %s: %w", path, uerr)
		}
	case errors.Is(err, os.ErrNotExist) && !explicit:
		// Default path is best-effort: fall through with zero values.
	default:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	auth := f.Icinga2
	if auth.BaseURL == "" {
		auth = f.IcingaWeb
	}

	s := &Settings{
		BaseURL:    auth.BaseURL,
		Username:   auth.Username,
		Password:   auth.Password,
		VerifyTLS:  derefBool(auth.VerifyTLS, true),
		CABundle:   auth.CABundle,
		ClientCert: auth.ClientCert,
		ClientKey:  auth.ClientKey,

		RequestTimeoutSec: derefFloat(f.HTTP.RequestTimeoutSec, 15.0),
		MaxRetries:        derefInt(f.HTTP.MaxRetries, 3),
		RetryBackoffSec:   derefFloat(f.HTTP.RetryBackoffSec, 0.5),

		HostSummaryFields: orDefault(f.SummaryFields.Host, []string{
			"name", "state", "state_type", "problem",
			"last_check", "last_state_change",
			"acknowledgement", "downtime_depth",
			"last_check_result.output",
		}),
		ServiceSummaryFields: orDefault(f.SummaryFields.Service, []string{
			"host_name", "name", "state", "state_type", "problem",
			"last_check", "last_state_change",
			"acknowledgement", "downtime_depth",
			"last_check_result.output",
		}),
		CommentSummaryFields: orDefault(f.SummaryFields.Comment, []string{
			"host_name", "service_name", "author", "text",
			"entry_time", "expire_time", "persistent", "sticky",
		}),
		DowntimeSummaryFields: orDefault(f.SummaryFields.Downtime, []string{
			"host_name", "service_name", "author", "comment",
			"start_time", "end_time", "fixed", "duration", "was_cancelled",
		}),

		Endpoints: defaultEndpoints(),
	}

	if s.BaseURL == "" {
		return nil, fmt.Errorf("icinga2.base_url is required (config: %s)", path)
	}
	if (s.ClientCert == "") != (s.ClientKey == "") {
		return nil, fmt.Errorf("icinga2.client_cert and icinga2.client_key must be set together")
	}
	return s, nil
}

func defaultEndpoints() map[string]string {
	return map[string]string{
		"hosts":             "v1/objects/hosts",
		"services":          "v1/objects/services",
		"downtimes":         "v1/objects/downtimes",
		"comments":          "v1/objects/comments",
		"hostgroups":        "v1/objects/hostgroups",
		"servicegroups":     "v1/objects/servicegroups",
		"schedule_downtime": "v1/actions/schedule-downtime",
	}
}

func (s *Settings) Endpoint(key string) (string, error) {
	v, ok := s.Endpoints[key]
	if !ok {
		return "", fmt.Errorf("unknown endpoint key %q", key)
	}
	return v, nil
}

func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func derefInt(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func derefFloat(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func orDefault(v, def []string) []string {
	if len(v) == 0 {
		return def
	}
	return v
}
