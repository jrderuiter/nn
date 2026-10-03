package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jrderuiter/nn/internal/tool"
)

// newExampleCmd prints a complete nn.toml.
//
// It prints rather than writes, so it never overwrites a configuration and can
// be piped wherever it is wanted.
func newExampleCmd() *cobra.Command {
	var out string
	c := &cobra.Command{
		Use:   "example",
		Short: "Print a complete example nn.toml",
		Long: "example prints a full nn.toml with every section and every setting,\n" +
			"commented. It writes nothing, so save it where you want it:\n\n" +
			"  nn example > nn.toml",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body := example()
			if out == "" {
				_, err := fmt.Fprint(cmd.OutOrStdout(), body)
				return err
			}
			if _, err := os.Stat(out); err == nil {
				return fmt.Errorf("%s already exists", out)
			}
			return os.WriteFile(out, []byte(body), 0o644)
		},
	}
	c.Flags().StringVarP(&out, "out", "o", "", "write to this file, which must not exist")
	return c
}

func example() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	p("# nn configuration. Every value below can also come from the environment,")
	p("# as NN_ plus the key path in upper case: NN_NONO_NETWORK_PROFILE,")
	p("# NN_TOOLS_KUBERNETES_CONTEXT, and so on.")
	p("#")
	p("# Writing a [tools.<name>] section is what turns that tool on. From the")
	p("# environment that is NN_TOOLS_<NAME>=true, and =false turns one off.")
	p("")
	p("[nono]")
	p("# nono profiles to extend, merged before the generated parts. Hand")
	p("# written mixins go here. An agent pack goes in its [agents.<name>] section.")
	p("extends = [\"default\"]")
	p("")
	p("# nono policy groups to include by name.")
	p("# groups = [\"unlink_protection\"]")
	p("")
	p("# One of nono's built in network allowlists: minimal, developer,")
	p("# claude-code, codex, opencode, enterprise. minimal grants the LLM APIs")
	p("# and nothing else. An empty value leaves egress unrestricted.")
	p("network_profile = \"minimal\"")
	p("")
	p("# Extra hosts, on top of the profile and whatever the tools allow.")
	p("# allow_domain = [\"proxy.golang.org\", \"*.internal.example.com\"]")
	p("")
	p("# A raw nono profile fragment, for anything with no tool of its own. It")
	p("# applies last, so it overrides the generated parts.")
	p("# [nono.profile.filesystem]")
	p("# read = [\"$HOME/.config/some-tool\"]")
	p("")
	p("# One section per agent, named after its command. A run of that command")
	p("# adds the section to [nono], and its network_profile replaces the one")
	p("# above. Any other command, such as kubectl, gets [nono] alone. Pull a")
	p("# pack with nono pull before you extend it.")
	p("# [agents.claude]")
	p("# extends = [\"nolabs-ai/claude\"]")
	p("# network_profile = \"claude-code\"")
	p("#")
	p("# [agents.agy]")
	p("# extends = [\"nolabs-ai/antigravity\"]")
	p("# allow_domain = [\"cloudcode-pa.googleapis.com\", \"oauth2.googleapis.com\"]")
	p("#")
	p("# A raw profile fragment for one agent. agy starts a language server on a")
	p("# random local port, which macOS picks from 49152 to 65535. The range keeps")
	p("# out the fixed ports of local services, such as databases.")
	p("# [agents.agy.profile.network]")
	p("# open_port_range = [[49152, 65535]]")
	p("")
	p("# Where secrets come from. nn never reads a secret itself: it tells nono to")
	p("# run fnox on the host when the proxy needs one.")
	p("# [fnox]")
	p("# binary = \"fnox\"")
	p("# config = \"fnox.toml\"")
	p("# profile = \"production\"")
	p("")
	p("# Language runtimes and version managers. Each adds one nono group plus the")
	p("# writable caches and variables that group leaves out. No settings.")
	for _, name := range tool.Runtimes() {
		p("# [tools.%s]", name)
	}
	p("")
	p("[tools.git]")
	p("# The committer identity inside the sandbox.")
	p("name = \"Your Name\"")
	p("email = \"you@example.com\"")
	p("# Extra git hosts to allow.")
	p("# hosts = [\"gitlab.com\"]")
	p("# Grant read access to the host git configuration.")
	p("# config = true")
	p("# Grant the shared git directory when the working directory is a linked")
	p("# worktree.")
	p("# worktree = true")
	p("")
	p("# GitHub. Needs a fnox key holding the token. git access, the rewrite of")
	p("# ssh remotes to HTTPS, and the gh redirect are all on unless turned off.")
	p("# [tools.github]")
	p("# secret = \"GITHUB_TOKEN\"")
	p("# git = false           # no clone, fetch or push over HTTPS")
	p("# rewrite_ssh = false   # leave ssh remotes alone, which breaks fetch")
	p("# gh_cli = false        # let gh use the host configuration")
	p("")
	p("# Azure DevOps. Needs a fnox key holding a personal access token. ssh")
	p("# remotes of the projects that this repository uses are rewritten to HTTPS.")
	p("# [tools.azure_devops]")
	p("# organization and project come from the remote when they are not set.")
	p("# organization = \"your-org\"")
	p("# project = \"Your Project\"   # the default project for az devops")
	p("# secret = \"AZURE_DEVOPS_PAT\"")
	p("# projects = [\"Other Project\"]   # more projects to clone over ssh")
	p("# rewrite_ssh = false   # leave ssh remotes alone, which breaks fetch")
	p("# az_cli = false        # let az use the host configuration")
	p("")
	p("# One Kubernetes cluster. nono mints a token on the host for a service")
	p("# account that already exists; nn never creates accounts or RBAC.")
	p("# [tools.kubernetes]")
	p("# auth = \"service-account\"   # required; or \"host\" to copy the context credentials in")
	p("# context = \"prod\"")
	p("# service_account = \"agent-reader\"")
	p("# service_account_namespace = \"agent-access\"   # default: default")
	p("# token_ttl = \"1h\"")
	p("# kubectl = \"/opt/homebrew/bin/kubectl\"   # a real binary, not a shim")
	p("# kubeconfig = \"~/.kube/config\"")
	p("# cluster_ca = \"ca.pem\"                   # when the context carries none")
	p("# allow_missing_ca = false")

	return b.String()
}
