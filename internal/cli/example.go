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
				fmt.Print(body)
				return nil
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
	p("# Commit this file, and put whatever differs per machine in nn.local.toml")
	p("# beside it, which merges over this one. Add that name to .gitignore.")
	p("")
	p("[nono]")
	p("# nono profiles to extend, merged before the generated parts. An agent")
	p("# pack such as nolabs-ai/claude goes here, with any hand written mixins.")
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
	p("# Where secrets come from. nn never reads a secret itself: it tells nono to")
	p("# run fnox on the host when the proxy needs one.")
	p("# [fnox]")
	p("# binary = \"fnox\"")
	p("# config = \"fnox.toml\"")
	p("# profile = \"production\"")
	p("")
	p("[tools]")
	p("# The tools this project uses. Only a tool named here is turned on: a")
	p("# [tools.<name>] section below configures a tool, but does not turn it on.")
	p("# NN_TOOLS_ENABLED=git,github replaces the list for one run. Available:")
	p("# %s.", strings.Join(tool.Known(), ", "))
	p("#")
	p("# Language runtimes and version managers need no section. Each adds one")
	p("# nono group plus the writable caches and variables that group leaves")
	p("# out: %s.", strings.Join(tool.Runtimes(), ", "))
	p("enabled = [\"git\"]")
	p("")
	p("[tools.git]")
	p("# The committer identity inside the sandbox.")
	p("name = \"Your Name\"")
	p("email = \"you@example.com\"")
	p("# Extra git hosts to allow.")
	p("# hosts = [\"gitlab.com\"]")
	p("# Grant read access to the host git configuration.")
	p("# config = true")
	p("")
	p("# GitHub. Needs a fnox key holding the token. git access, the rewrite of")
	p("# ssh remotes to HTTPS, and the gh redirect are all on unless turned off.")
	p("# [tools.github]")
	p("# secret = \"GITHUB_TOKEN\"")
	p("# git = false           # no clone, fetch or push over HTTPS")
	p("# rewrite_ssh = false   # leave ssh remotes alone, which breaks fetch")
	p("# gh_cli = false        # let gh use the host configuration")
	p("")
	p("# One Kubernetes cluster. nono mints a token on the host for a service")
	p("# account that already exists; nn never creates accounts or RBAC.")
	p("# [tools.kubernetes]")
	p("# context = \"prod\"")
	p("# service_account = \"agent-reader\"")
	p("# service_account_namespace = \"agent-access\"   # default: default")
	p("# token_ttl = \"1h\"")
	p("# kubectl = \"/opt/homebrew/bin/kubectl\"   # a real binary, not a shim")
	p("# kubeconfig = \"~/.kube/config\"")
	p("# cluster_ca = \"ca.pem\"                   # when the context carries none")
	p("# allow_missing_ca = false")
	p("#")
	p("# In a pod, the access comes from the mounted service account instead of")
	p("# a kubeconfig. Set this from the pod spec, as")
	p("# NN_TOOLS_KUBERNETES_IN_CLUSTER=true, and one file serves both places.")
	p("# It refuses context, kubeconfig, cluster_ca and allow_missing_ca, which")
	p("# describe a kubeconfig that a pod does not have.")
	p("# in_cluster = true")
	p("# api_server = \"https://kubernetes.default.svc\"   # the default")
	p("# service_account_dir = \"/var/run/secrets/kubernetes.io/serviceaccount\"")

	return b.String()
}
