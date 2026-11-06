/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package demo contains the real Polaris and A2A example components.
package demo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
)

const (
	Owner            = "go-agent-naming-polarismesh-e2e"
	OwnerLabel       = "e2e.owner"
	RunLabel         = "e2e.run.id"
	ServicePrefix    = "go-agent-e2e-"
	SkillPrefix      = "gan-e2e-"
	DefaultNamespace = "go-agent-naming-e2e"
	FixtureAPIKey    = "local-e2e-fixture-key"
	FixtureKeyHeader = "X-E2E-Key"
)

var safeSegment = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,100}$`)

// Options configures one example process. Credentials are never serialized.
type Options struct {
	Mode, Server, Discover, SkillAddress, Console, ConfigFile string
	Namespace, RunID, RunDir, StateFile, ResourcePolicy       string
	Service, Node, ReadyFile, SkillName, SkillVersion, Path   string
	Host, MetadataMode, Tenant, ProviderBinary, Message       string
	Timeout, RunTimeout                                       time.Duration
	Stream, Debug, Apply, DryRun, RemoveArtifacts             bool
	DeleteServices                                            bool
}

// Environment returns the first nonempty environment value.
func Environment(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func envDefault(key, fallback string) string {
	if value := Environment(key); value != "" {
		return value
	}
	return fallback
}

// ParseOptions defines flags shared by the runnable examples.
func ParseOptions(mode string) (Options, error) {
	return parseOptions(mode, os.Args[1:])
}

func parseOptions(mode string, args []string) (Options, error) {
	o := Options{Mode: mode}
	flag := flag.NewFlagSet(mode, flag.ContinueOnError)
	flag.StringVar(&o.Mode, "mode", mode, "verify or cleanup (E2E executable)")
	flag.StringVar(&o.Server, "server", Environment("POLARIS_SERVER"), "Polaris host")
	flag.StringVar(&o.Discover, "discover", Environment("POLARIS_SERVER_ADDRESS"), "Discovery gRPC host:port")
	flag.StringVar(&o.SkillAddress, "skill-address", Environment("POLARIS_SKILL_ADDRESS"), "Skill gRPC host:port")
	flag.StringVar(&o.Console, "console", Environment("POLARIS_CONSOLE_ADDRESS"), "Console HTTP(S) origin")
	flag.StringVar(&o.ConfigFile, "config-file", Environment("POLARIS_E2E_CONFIG"), "Existing SDK configuration file")
	flag.StringVar(&o.Namespace, "namespace", envDefault("POLARIS_E2E_NAMESPACE", DefaultNamespace), "Isolated non-default namespace")
	flag.StringVar(&o.RunID, "run-id", "", "Resource ownership identifier")
	flag.StringVar(&o.RunDir, "run-dir", "", "Run-specific logs, state and caches")
	flag.StringVar(&o.StateFile, "state", "", "Explicit cleanup state file")
	flag.StringVar(&o.ResourcePolicy, "resources", Environment("POLARIS_E2E_RESOURCES"), "Required for verification: keep or cleanup")
	flag.StringVar(&o.Service, "service", "", "Polaris service name")
	flag.StringVar(&o.Node, "node", "provider", "Provider node label or consumer caller key")
	flag.StringVar(&o.ReadyFile, "ready", "", "Provider readiness state file")
	flag.StringVar(&o.SkillName, "skill-name", "", "Published Skill to load into the provider")
	flag.StringVar(&o.SkillVersion, "skill-version", "1.0.0", "Explicit published Skill version")
	flag.StringVar(&o.Path, "a2a-path", "/a2a", "Exact A2A JSON-RPC path")
	flag.StringVar(&o.Host, "advertise-host", "127.0.0.1", "Provider address reachable by the consumer")
	flag.StringVar(&o.MetadataMode, "metadata-mode", "valid", "valid, missing, or mismatch fault fixture")
	flag.StringVar(&o.Tenant, "tenant", "e2e-tenant", "Advertised A2A interface tenant")
	flag.StringVar(&o.ProviderBinary, "provider-binary", "", "Built provider executable used by the suite")
	flag.StringVar(&o.Message, "message", "hello", "Consumer message")
	flag.DurationVar(&o.Timeout, "timeout", 5*time.Second, "Per-operation timeout")
	flag.DurationVar(&o.RunTimeout, "run-timeout", 12*time.Minute, "Complete suite deadline")
	flag.BoolVar(&o.Stream, "stream", false, "Use streaming A2A")
	flag.BoolVar(&o.Debug, "debug", false, "Enable SDK debug logs")
	flag.BoolVar(&o.Apply, "apply", false, "Apply cleanup after ownership validation")
	force := false
	flag.BoolVar(&force, "force", false, "Alias for -apply")
	flag.BoolVar(&o.DryRun, "dry-run", false, "Show cleanup without changing resources")
	flag.BoolVar(&o.RemoveArtifacts, "artifacts", false, "Remove only this run's local caches and logs after cleanup")
	deleteServiceAlias := false
	flag.BoolVar(&deleteServiceAlias, "delete-service", false,
		"Delete this run's services and Skills, plus historical empty owned services")
	flag.BoolVar(&o.DeleteServices, "delete-services", false,
		"Alias for -delete-service")
	if err := flag.Parse(args); err != nil {
		return o, err
	}
	o.Apply = o.Apply || force
	o.DeleteServices = o.DeleteServices || deleteServiceAlias
	if len(flag.Args()) != 0 {
		return o, errors.New("unexpected positional arguments")
	}
	if mode == "verify" {
		if o.Mode != "verify" && o.Mode != "cleanup" && o.Mode != "inspect" {
			return o, errors.New("E2E executable only supports verify, cleanup, or inspect mode")
		}
	} else if o.Mode != mode {
		return o, errors.New("mode must match the example executable")
	}
	err := o.Validate()
	return o, err
}

// Validate checks addresses and ownership before any external writes.
func (o *Options) Validate() error {
	if o.Mode != "cleanup" && (o.Apply || o.DryRun || o.RemoveArtifacts || o.DeleteServices) {
		return errors.New("cleanup flags are only valid in cleanup mode")
	}
	if o.Mode == "cleanup" {
		if o.StateFile == "" && !o.DeleteServices {
			return fmt.Errorf("cleanup requires -state or -delete-services")
		}
		if o.StateFile == "" && o.RemoveArtifacts {
			return fmt.Errorf("cleanup artifacts requires -state")
		}
		if o.Timeout <= 0 {
			return fmt.Errorf("timeout must be positive")
		}
		if o.DeleteServices {
			if !safeSegment.MatchString(o.Namespace) || strings.EqualFold(o.Namespace, "default") {
				return fmt.Errorf("use a safe, non-default E2E namespace")
			}
			if o.Console == "" {
				if o.Server == "" {
					return errors.New("delete-services requires -server, POLARIS_SERVER, or -console")
				}
				o.Console = "http://" + net.JoinHostPort(o.Server, "8090")
			}
			if err := ValidateConsole(o.Console); err != nil {
				return err
			}
		}
		return nil
	}
	if !safeSegment.MatchString(o.Namespace) || strings.EqualFold(o.Namespace, "default") {
		return fmt.Errorf("use a safe, non-default E2E namespace")
	}
	if o.Server == "" || strings.ContainsAny(o.Server, "/?#@ \t\r\n") {
		return fmt.Errorf("server must be a host without a URL or credentials")
	}
	if o.Discover == "" {
		o.Discover = net.JoinHostPort(o.Server, "8091")
	}
	if o.SkillAddress == "" {
		o.SkillAddress = net.JoinHostPort(o.Server, "8094")
	}
	if o.Console == "" {
		o.Console = "http://" + net.JoinHostPort(o.Server, "8090")
	}
	for _, address := range []string{o.Discover, o.SkillAddress} {
		host, port, err := net.SplitHostPort(address)
		number, portErr := strconv.Atoi(port)
		if err != nil || host == "" || strings.ContainsAny(host, "/?#@ \t\r\n") || portErr != nil || number < 1 || number > 65535 {
			return fmt.Errorf("gRPC addresses must be host:port")
		}
	}
	if err := ValidateConsole(o.Console); err != nil {
		return err
	}
	if o.Timeout <= 0 || o.RunTimeout <= o.Timeout {
		return fmt.Errorf("positive run-timeout must exceed timeout")
	}
	if o.RunID == "" {
		var entropy [4]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return err
		}
		o.RunID = time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(entropy[:])
	}
	if !safeSegment.MatchString(o.RunID) || len(o.RunID) > 32 {
		return fmt.Errorf("run-id must be a safe identifier of at most 32 characters")
	}
	if o.RunDir == "" {
		o.RunDir = filepath.Join(".logs", o.RunID)
	}
	abs, err := filepath.Abs(o.RunDir)
	if err != nil {
		return err
	}
	o.RunDir = abs
	if o.Mode == "verify" && filepath.Base(o.RunDir) != o.RunID {
		return fmt.Errorf("verification run-dir must end with run-id for scoped cleanup")
	}
	switch o.Mode {
	case "verify":
		if o.ResourcePolicy != "keep" && o.ResourcePolicy != "cleanup" {
			return fmt.Errorf("choose -resources=keep or -resources=cleanup explicitly")
		}
		if o.ProviderBinary == "" {
			return fmt.Errorf("verification requires -provider-binary")
		}
	case "provider":
		if o.Service == "" || o.ReadyFile == "" {
			return fmt.Errorf("provider requires -service and -ready")
		}
	case "consumer":
		if o.Service == "" {
			return fmt.Errorf("consumer requires -service")
		}
	case "inspect":
		if o.Service == "" && o.SkillName == "" {
			return errors.New("inspect requires -service or -skill-name")
		}
	default:
		return fmt.Errorf("unknown example mode")
	}
	return nil
}

// ValidateConsole rejects access-bearing origins before they reach state/logs.
func ValidateConsole(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("console must be an HTTP(S) origin without credentials, query or path")
	}
	return nil
}

// LoadConfiguration applies example overrides without persisting credentials.
func LoadConfiguration(o Options, enableSkills bool) (config.Configuration, error) {
	var cfg config.Configuration = config.NewDefaultConfiguration([]string{o.Discover})
	if o.ConfigFile != "" {
		loaded, err := config.LoadConfigurationByFile(o.ConfigFile)
		if err != nil {
			return nil, fmt.Errorf("cannot load the supplied SDK configuration")
		}
		cfg = loaded
		cfg.SetDefault()
	}
	cfg.GetGlobal().GetServerConnector().SetAddresses([]string{o.Discover})
	if token := Environment("POLARIS_TOKEN"); token != "" {
		cfg.GetGlobal().GetServerConnector().SetToken(token)
	}
	cfg.GetGlobal().GetAPI().SetTimeout(o.Timeout)
	cfg.GetGlobal().GetAPI().SetMaxRetryTimes(0)
	cfg.GetGlobal().GetAdmin().SetPort(0)
	cfg.GetGlobal().GetStatReporter().SetEnable(false)
	cfg.GetGlobal().GetClient().GetLabels()["CLIENT_ID"] = Owner + "-" + o.RunID + "-" + o.Node
	cfg.GetConsumer().GetLocalCache().SetPersistEnable(false)
	cfg.GetConsumer().GetLocalCache().SetServiceRefreshInterval(500 * time.Millisecond)
	cfg.GetConfigFile().GetLocalCache().SetPersistEnable(false)
	cfg.GetSkill().SetEnable(enableSkills)
	cfg.GetSkill().SetAddresses([]string{o.SkillAddress})
	cfg.GetSkill().SetConnectTimeout(o.Timeout)
	cfg.GetSkill().SetMessageTimeout(o.Timeout)
	if token := Environment("POLARIS_SKILL_TOKEN", "POLARIS_TOKEN"); token != "" {
		cfg.GetSkill().SetToken(token)
	}
	cfg.GetServiceWriter().SetAddresses([]string{o.Console})
	cfg.GetServiceWriter().SetTimeout(o.Timeout)
	if token := Environment("POLARIS_CONSOLE_TOKEN", "POLARIS_TOKEN", "POLARIS_SERVICE_TOKEN"); token != "" {
		cfg.GetServiceWriter().SetToken(token)
	}
	return cfg, nil
}

// NewSDK creates one process-owned SDK context.
func NewSDK(o Options, enableSkills bool) (api.SDKContext, error) {
	cfg, err := LoadConfiguration(o, enableSkills)
	if err != nil {
		return nil, err
	}
	return api.InitContextByConfig(cfg)
}

// LoadConfigurationForCleanup reads credentials from an optional existing file.
func LoadConfigurationForCleanup(o Options) (config.Configuration, error) {
	if o.ConfigFile == "" {
		return nil, nil
	}
	cfg, err := config.LoadConfigurationByFile(o.ConfigFile)
	if err != nil {
		return nil, errors.New("cannot load cleanup SDK configuration")
	}
	cfg.SetDefault()
	return cfg, nil
}

// HTTPClient uses direct connections to this explicitly configured deployment.
func HTTPClient(timeout time.Duration) (*http.Client, *http.Transport) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport
}

// SignalContext keeps the signal goroutine owned by the executable's main.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
