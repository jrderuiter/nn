package config

// Section holds every key nn understands. The same type is used for the
// top-level shared keys and for each per-mode block, so there is exactly one
// schema to maintain.
//
// Naming rule: keys mirror nono's flag names with '-' replaced by '_'. The
// mirroring is deliberate — `nono run --help` is the reference doc for this
// file. nono ships both --rollback and --no-rollback (and both halves of
// --audit-integrity), so rewriting the negative flags into positive keys would
// make it impossible to say which of the two you meant.
//
// The one exception is shell_bin, which carries nono's --shell: the key
// `shell` is taken by the shell-mode block.
//
// Only flags a nono profile cannot express appear here. Anything settable in
// profile.json (filesystem grants, network policy, credentials, ports) is
// deliberately absent — nn does not duplicate the profile.
type Section struct {
	// --- nn's own keys; no nono flag behind them ---
	Wrappers [][]string        `yaml:"wrappers"`
	Command  []string          `yaml:"command"`
	NonoBin  Str               `yaml:"nono_bin"`
	NonoArgs []string          `yaml:"nono_args"`
	Env      map[string]string `yaml:"env"`
	EnvUnset []string          `yaml:"env_unset"`
	Herdr    Bool              `yaml:"herdr"`

	// --- profile selection ---
	Profile Str      `yaml:"profile"`
	Extends []string `yaml:"extends"`
	Config  Str      `yaml:"config"`

	// --- filesystem / workdir ---
	AllowCwd                   Bool     `yaml:"allow_cwd"`
	Workdir                    Str      `yaml:"workdir"`
	BypassProtection           []string `yaml:"bypass_protection"`
	SuppressSavePrompt         []string `yaml:"suppress_save_prompt"`
	AllowUnixSocket            []string `yaml:"allow_unix_socket"`
	AllowUnixSocketBind        []string `yaml:"allow_unix_socket_bind"`
	AllowUnixSocketDir         []string `yaml:"allow_unix_socket_dir"`
	AllowUnixSocketDirBind     []string `yaml:"allow_unix_socket_dir_bind"`
	AllowUnixSocketSubtree     []string `yaml:"allow_unix_socket_subtree"`
	AllowUnixSocketSubtreeBind []string `yaml:"allow_unix_socket_subtree_bind"`

	// --- proxy / CA ---
	TrustProxyCA    Bool `yaml:"trust_proxy_ca"`
	ProxyCAValidity Int  `yaml:"proxy_ca_validity"`
	ProxyPort       Int  `yaml:"proxy_port"`

	// --- diagnostics / output ---
	NoDiagnostics   Bool  `yaml:"no_diagnostics"`
	DiagnosticsJSON Bool  `yaml:"diagnostics_json"`
	DryRun          Bool  `yaml:"dry_run"`
	Verbose         Count `yaml:"verbose"`
	Silent          Bool  `yaml:"silent"`
	Theme           Str   `yaml:"theme"`
	LogFile         Str   `yaml:"log_file"`

	// --- session ---
	Detached       Bool `yaml:"detached"`
	DetachTimeout  Int  `yaml:"detach_timeout"`
	Name           Str  `yaml:"name"`
	StartupTimeout Int  `yaml:"startup_timeout"`
	ShellBin       Str  `yaml:"shell_bin"`

	// --- permissions / hardening ---
	StrictBrokerPath    Bool `yaml:"strict_broker_path"`
	AllowLaunchServices Bool `yaml:"allow_launch_services"`
	AllowGPU            Bool `yaml:"allow_gpu"`
	CapabilityElevation Bool `yaml:"capability_elevation"`

	// --- resources ---
	SkipDir      []string `yaml:"skip_dir"`
	Memory       Str      `yaml:"memory"`
	MaxProcesses Int      `yaml:"max_processes"`

	// --- rollback / audit ---
	Rollback         Bool     `yaml:"rollback"`
	NoRollback       Bool     `yaml:"no_rollback"`
	NoRollbackPrompt Bool     `yaml:"no_rollback_prompt"`
	RollbackExclude  []string `yaml:"rollback_exclude"`
	RollbackInclude  []string `yaml:"rollback_include"`
	RollbackAll      Bool     `yaml:"rollback_all"`
	RollbackDest     Str      `yaml:"rollback_dest"`
	NoAudit          Bool     `yaml:"no_audit"`
	AuditIntegrity   Bool     `yaml:"audit_integrity"`
	NoAuditIntegrity Bool     `yaml:"no_audit_integrity"`
	AuditSignKey     Str      `yaml:"audit_sign_key"`
	TrustOverride    Bool     `yaml:"trust_override"`
}

// File is a parsed .nono/nn.yml: shared keys inline at the top level, plus an
// optional block per mode. A block's values override the top level; see
// Resolve.
type File struct {
	Section `yaml:",inline"`

	Run   *Section `yaml:"run"`
	Shell *Section `yaml:"shell"`
	Wrap  *Section `yaml:"wrap"`

	// Mode catches the key removed in favour of subcommands, purely so the
	// error can say what to do instead of "unknown key".
	Mode *string `yaml:"mode"`
}

// Block returns the per-mode section, or nil when the file has none.
func (f *File) Block(m Mode) *Section {
	switch m {
	case ModeRun:
		return f.Run
	case ModeShell:
		return f.Shell
	case ModeWrap:
		return f.Wrap
	}
	return nil
}
