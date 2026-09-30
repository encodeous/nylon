package cmd

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

type initOptions struct {
	id               string
	port             uint16
	key              string
	output           string
	force            bool
	useSystemRouting bool
	noNetConfigure   bool
	dnsResolvers     []string
	interfaceName    string
	logPath          string
	unexcludeIPs     []string
	excludeIPs       []string
	preUp            []string
	preDown          []string
	postUp           []string
	postDown         []string
	serve            bool
	connect          string
	token            string
	central          string
	addresses        []string
	prefixes         []string
}

func newInitCmd() *cobra.Command {
	opts := initOptions{}
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Generate a node configuration",
		GroupID: "init",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.connect == "" && (len(opts.addresses) != 0 || len(opts.prefixes) != 0) {
				return errors.New("--address and --prefix are only used with --connect")
			}
			cmd.SilenceUsage = true
			if opts.serve {
				return runInitServe(cmd, opts)
			}
			if opts.connect != "" {
				return runInitConnect(cmd, opts)
			}
			cfg, err := buildNodeConfig(opts)
			if err != nil {
				return err
			}
			if err = writeNodeConfig(cfg, opts.output, opts.force); err != nil {
				return err
			}

			publicKey, err := cfg.Key.Pubkey().MarshalText()
			if err != nil {
				return fmt.Errorf("encode public key: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\nPublic key: %s\n", opts.output, publicKey)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.id, "id", "", "Unique node ID (defaults to hostname)")
	flags.Uint16Var(&opts.port, "port", state.DefaultPort, "UDP port Nylon listens on")
	flags.StringVar(&opts.key, "key", "", "Existing private key (a new key is generated if omitted)")
	flags.StringVarP(&opts.output, "output", "o", DefaultNodeConfigPath, "Node config output path")
	flags.BoolVar(&opts.force, "force", false, "Overwrite the output file if it exists")
	flags.BoolVar(&opts.useSystemRouting, "use-system-routing", false, "Route peer packets through the system")
	flags.BoolVar(&opts.noNetConfigure, "no-net-configure", false, "Do not configure system networking")
	flags.StringSliceVar(&opts.dnsResolvers, "dns-resolver", nil, "DNS resolver in ip:port form (repeatable)")
	flags.StringVar(&opts.interfaceName, "interface-name", "", "Nylon interface name")
	flags.StringVar(&opts.logPath, "log-path", "", "Log file path")
	flags.StringSliceVar(&opts.unexcludeIPs, "unexclude-ip", nil, "Centrally excluded IP prefix to include (repeatable)")
	flags.StringSliceVar(&opts.excludeIPs, "exclude-ip", nil, "IP prefix to exclude (repeatable)")
	flags.StringSliceVar(&opts.preUp, "pre-up", nil, "Command to run before interface startup (repeatable)")
	flags.StringSliceVar(&opts.preDown, "pre-down", nil, "Command to run before interface shutdown (repeatable)")
	flags.StringSliceVar(&opts.postUp, "post-up", nil, "Command to run after interface startup (repeatable)")
	flags.StringSliceVar(&opts.postDown, "post-down", nil, "Command to run after interface shutdown (repeatable)")
	flags.BoolVar(&opts.serve, "serve", false, "Print a setup token and wait for a node to join with --connect")
	flags.StringVar(&opts.connect, "connect", "", "Join the network through a node running --serve (host[:port])")
	flags.StringVar(&opts.token, "token", "", "Setup token printed by --serve")
	flags.StringVarP(&opts.central, "config", "c", DefaultConfigPath, "Central config path used by --serve and --connect")
	flags.StringSliceVar(&opts.addresses, "address", nil, "Nylon address to join with, used by --connect (repeatable)")
	flags.StringSliceVar(&opts.prefixes, "prefix", nil, "IP prefix to advertise, used by --connect (repeatable)")
	cmd.MarkFlagsMutuallyExclusive("serve", "connect")
	cmd.MarkFlagsRequiredTogether("connect", "token")
	return cmd
}

func writeNodeConfig(cfg *state.LocalCfg, path string, force bool) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode node config: %w", err)
	}
	return writeSecretFile(path, data, force)
}

func writeSecretFile(path string, data []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists (use --force to overwrite)", path)
		}
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err = file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure %s: %w", path, err)
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func buildNodeConfig(opts initOptions) (*state.LocalCfg, error) {
	privateKey := state.GenerateKey()
	if opts.key != "" {
		if err := privateKey.UnmarshalText([]byte(opts.key)); err != nil {
			return nil, fmt.Errorf("invalid private key: %w", err)
		}
	}

	id := opts.id
	if id == "" {
		host, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("--id is required: %w", err)
		}
		id = strings.ToLower(host)
	}

	cfg := &state.LocalCfg{
		Key:              privateKey,
		Id:               state.NodeId(id),
		Port:             opts.port,
		UseSystemRouting: opts.useSystemRouting,
		NoNetConfigure:   opts.noNetConfigure,
		DnsResolvers:     opts.dnsResolvers,
		InterfaceName:    opts.interfaceName,
		LogPath:          opts.logPath,
		PreUp:            opts.preUp,
		PreDown:          opts.preDown,
		PostUp:           opts.postUp,
		PostDown:         opts.postDown,
	}

	var err error
	if cfg.UnexcludeIPs, err = parsePrefixes(opts.unexcludeIPs); err != nil {
		return nil, fmt.Errorf("invalid --unexclude-ip: %w", err)
	}
	if cfg.ExcludeIPs, err = parsePrefixes(opts.excludeIPs); err != nil {
		return nil, fmt.Errorf("invalid --exclude-ip: %w", err)
	}

	if err := state.NodeConfigValidator(nil, cfg); err != nil {
		return nil, fmt.Errorf("invalid node config: %w", err)
	}
	return cfg, nil
}

func parseAddrs(values []string) ([]netip.Addr, error) {
	addrs := make([]netip.Addr, 0, len(values))
	for _, value := range values {
		addr, err := netip.ParseAddr(value)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", value, err)
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

func parsePrefixes(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", value, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func init() {
	rootCmd.AddCommand(newInitCmd())
}
