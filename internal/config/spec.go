package config

// Kind is how a flag's value turns into argv.
type Kind uint8

const (
	KindSwitch      Kind = iota // bare flag, emitted when the value is true
	KindString                  // --flag VALUE
	KindInt                     // --flag N
	KindStringSlice             // --flag v1 --flag v2
	KindCount                   // -v repeated N times
)

// Anchor says what a path-shaped value is relative to. nono runs from the
// user's CWD, which may be far below the project root, so every path nn emits
// is made absolute first.
type Anchor uint8

const (
	AnchorNone   Anchor = iota // not a path; pass through untouched
	AnchorConfig               // relative to the config's own dir (profile, manifest)
	AnchorRoot                 // relative to the project root
)

// FlagSpec describes one nono flag nn can emit. This table is the only place
// the per-mode matrix lives; emission, validation, scaffolding and doctor all
// derive from it.
type FlagSpec struct {
	Key     string // YAML key
	Flag    string // nono flag, e.g. "--allow-cwd"
	Kind    Kind
	Modes   Mode // modes that accept this flag
	Anchor  Anchor
	Sandbox bool // part of the sandbox spec, so exclusive with nono's -c
	Get     func(*Section) any
}

// Specs is ordered, and that order is the argv order. Keeping it fixed at
// compile time makes output byte-stable, which is what lets `nn print` be
// diffed across config edits and golden files stay readable.
//
// Modes were generated from `nono {run,shell,wrap} --help` on v0.76.0 rather
// than transcribed by hand.
var Specs = []FlagSpec{
	// profile selection
	{Key: "profile", Flag: "--profile", Kind: KindString, Modes: ModeAll, Anchor: AnchorConfig, Sandbox: true,
		Get: func(s *Section) any { return s.Profile }},
	{Key: "extends", Flag: "--extends", Kind: KindStringSlice, Modes: ModeAll, Sandbox: true,
		Get: func(s *Section) any { return s.Extends }},
	{Key: "config", Flag: "--config", Kind: KindString, Modes: ModeAll, Anchor: AnchorConfig,
		Get: func(s *Section) any { return s.Config }},

	// filesystem / workdir
	{Key: "allow_cwd", Flag: "--allow-cwd", Kind: KindSwitch, Modes: ModeAll, Sandbox: true,
		Get: func(s *Section) any { return s.AllowCwd }},
	{Key: "workdir", Flag: "--workdir", Kind: KindString, Modes: ModeAll, Anchor: AnchorRoot, Sandbox: true,
		Get: func(s *Section) any { return s.Workdir }},
	{Key: "bypass_protection", Flag: "--bypass-protection", Kind: KindStringSlice, Modes: ModeAll, Anchor: AnchorRoot, Sandbox: true,
		Get: func(s *Section) any { return s.BypassProtection }},
	{Key: "suppress_save_prompt", Flag: "--suppress-save-prompt", Kind: KindStringSlice, Modes: ModeAll, Anchor: AnchorRoot, Sandbox: true,
		Get: func(s *Section) any { return s.SuppressSavePrompt }},
	{Key: "allow_unix_socket", Flag: "--allow-unix-socket", Kind: KindStringSlice, Modes: ModeAll, Anchor: AnchorRoot, Sandbox: true,
		Get: func(s *Section) any { return s.AllowUnixSocket }},
	{Key: "allow_unix_socket_bind", Flag: "--allow-unix-socket-bind", Kind: KindStringSlice, Modes: ModeAll, Anchor: AnchorRoot, Sandbox: true,
		Get: func(s *Section) any { return s.AllowUnixSocketBind }},
	{Key: "allow_unix_socket_dir", Flag: "--allow-unix-socket-dir", Kind: KindStringSlice, Modes: ModeAll, Anchor: AnchorRoot, Sandbox: true,
		Get: func(s *Section) any { return s.AllowUnixSocketDir }},
	{Key: "allow_unix_socket_dir_bind", Flag: "--allow-unix-socket-dir-bind", Kind: KindStringSlice, Modes: ModeAll, Anchor: AnchorRoot, Sandbox: true,
		Get: func(s *Section) any { return s.AllowUnixSocketDirBind }},
	{Key: "allow_unix_socket_subtree", Flag: "--allow-unix-socket-subtree", Kind: KindStringSlice, Modes: ModeAll, Anchor: AnchorRoot, Sandbox: true,
		Get: func(s *Section) any { return s.AllowUnixSocketSubtree }},
	{Key: "allow_unix_socket_subtree_bind", Flag: "--allow-unix-socket-subtree-bind", Kind: KindStringSlice, Modes: ModeAll, Anchor: AnchorRoot, Sandbox: true,
		Get: func(s *Section) any { return s.AllowUnixSocketSubtreeBind }},

	// proxy / CA  (wrap is direct mode: no supervisor, so no proxy)
	{Key: "trust_proxy_ca", Flag: "--trust-proxy-ca", Kind: KindSwitch, Modes: ModeRun | ModeShell,
		Get: func(s *Section) any { return s.TrustProxyCA }},
	{Key: "proxy_ca_validity", Flag: "--proxy-ca-validity", Kind: KindInt, Modes: ModeRun | ModeShell,
		Get: func(s *Section) any { return s.ProxyCAValidity }},
	{Key: "proxy_port", Flag: "--proxy-port", Kind: KindInt, Modes: ModeRun | ModeShell,
		Get: func(s *Section) any { return s.ProxyPort }},

	// permissions / hardening
	{Key: "strict_broker_path", Flag: "--strict-broker-path", Kind: KindSwitch, Modes: ModeRun | ModeShell, Sandbox: true,
		Get: func(s *Section) any { return s.StrictBrokerPath }},
	{Key: "allow_launch_services", Flag: "--allow-launch-services", Kind: KindSwitch, Modes: ModeAll, Sandbox: true,
		Get: func(s *Section) any { return s.AllowLaunchServices }},
	{Key: "allow_gpu", Flag: "--allow-gpu", Kind: KindSwitch, Modes: ModeAll, Sandbox: true,
		Get: func(s *Section) any { return s.AllowGPU }},
	{Key: "capability_elevation", Flag: "--capability-elevation", Kind: KindSwitch, Modes: ModeRun, Sandbox: true,
		Get: func(s *Section) any { return s.CapabilityElevation }},

	// session
	{Key: "name", Flag: "--name", Kind: KindString, Modes: ModeRun | ModeShell,
		Get: func(s *Section) any { return s.Name }},
	{Key: "detached", Flag: "--detached", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.Detached }},
	{Key: "detach_timeout", Flag: "--detach-timeout", Kind: KindInt, Modes: ModeRun,
		Get: func(s *Section) any { return s.DetachTimeout }},
	{Key: "startup_timeout", Flag: "--startup-timeout", Kind: KindInt, Modes: ModeRun | ModeShell,
		Get: func(s *Section) any { return s.StartupTimeout }},
	{Key: "shell_bin", Flag: "--shell", Kind: KindString, Modes: ModeShell,
		Get: func(s *Section) any { return s.ShellBin }},

	// resources
	{Key: "memory", Flag: "--memory", Kind: KindString, Modes: ModeRun | ModeShell,
		Get: func(s *Section) any { return s.Memory }},
	{Key: "max_processes", Flag: "--max-processes", Kind: KindInt, Modes: ModeRun | ModeShell,
		Get: func(s *Section) any { return s.MaxProcesses }},
	{Key: "skip_dir", Flag: "--skip-dir", Kind: KindStringSlice, Modes: ModeRun,
		Get: func(s *Section) any { return s.SkipDir }},

	// rollback / audit
	{Key: "rollback", Flag: "--rollback", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.Rollback }},
	{Key: "no_rollback", Flag: "--no-rollback", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.NoRollback }},
	{Key: "no_rollback_prompt", Flag: "--no-rollback-prompt", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.NoRollbackPrompt }},
	{Key: "rollback_all", Flag: "--rollback-all", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.RollbackAll }},
	{Key: "rollback_dest", Flag: "--rollback-dest", Kind: KindString, Modes: ModeRun, Anchor: AnchorRoot,
		Get: func(s *Section) any { return s.RollbackDest }},
	{Key: "rollback_exclude", Flag: "--rollback-exclude", Kind: KindStringSlice, Modes: ModeRun,
		Get: func(s *Section) any { return s.RollbackExclude }},
	{Key: "rollback_include", Flag: "--rollback-include", Kind: KindStringSlice, Modes: ModeRun,
		Get: func(s *Section) any { return s.RollbackInclude }},
	{Key: "no_audit", Flag: "--no-audit", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.NoAudit }},
	{Key: "audit_integrity", Flag: "--audit-integrity", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.AuditIntegrity }},
	{Key: "no_audit_integrity", Flag: "--no-audit-integrity", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.NoAuditIntegrity }},
	{Key: "audit_sign_key", Flag: "--audit-sign-key", Kind: KindString, Modes: ModeRun,
		Get: func(s *Section) any { return s.AuditSignKey }},
	{Key: "trust_override", Flag: "--trust-override", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.TrustOverride }},

	// diagnostics / output
	{Key: "no_diagnostics", Flag: "--no-diagnostics", Kind: KindSwitch, Modes: ModeRun | ModeWrap,
		Get: func(s *Section) any { return s.NoDiagnostics }},
	{Key: "diagnostics_json", Flag: "--diagnostics-json", Kind: KindSwitch, Modes: ModeRun,
		Get: func(s *Section) any { return s.DiagnosticsJSON }},
	{Key: "dry_run", Flag: "--dry-run", Kind: KindSwitch, Modes: ModeAll,
		Get: func(s *Section) any { return s.DryRun }},
	{Key: "silent", Flag: "--silent", Kind: KindSwitch, Modes: ModeAll,
		Get: func(s *Section) any { return s.Silent }},
	{Key: "verbose", Flag: "--verbose", Kind: KindCount, Modes: ModeAll,
		Get: func(s *Section) any { return s.Verbose }},
	{Key: "theme", Flag: "--theme", Kind: KindString, Modes: ModeAll,
		Get: func(s *Section) any { return s.Theme }},
	{Key: "log_file", Flag: "--log-file", Kind: KindString, Modes: ModeAll, Anchor: AnchorRoot,
		Get: func(s *Section) any { return s.LogFile }},
}

// NNOnlyKeys are Section fields with no nono flag behind them. They are
// exempt from the spec-coverage check.
var NNOnlyKeys = []string{
	"wrappers", "command", "nono_bin", "nono_args", "env", "env_unset", "herdr",
}

// SpecByKey indexes Specs for lookups by YAML key.
var SpecByKey = func() map[string]*FlagSpec {
	m := make(map[string]*FlagSpec, len(Specs))
	for i := range Specs {
		m[Specs[i].Key] = &Specs[i]
	}
	return m
}()
