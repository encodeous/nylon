package cmd

import (
	"errors"
	"fmt"
	"net/netip"
	"os"

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
	distURL          string
	distKey          string
	unexcludeIPs     []string
	excludeIPs       []string
	preUp            []string
	preDown          []string
	postUp           []string
	postDown         []string
}

func newInitCmd() *cobra.Command {
	opts := initOptions{}
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Generate a node configuration",
		GroupID: "init",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := buildNodeConfig(opts)
			if err != nil {
				return err
			}

			data, err := yaml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("encode node config: %w", err)
			}

			flags := os.O_WRONLY | os.O_CREATE
			if opts.force {
				flags |= os.O_TRUNC
			} else {
				flags |= os.O_EXCL
			}
			file, err := os.OpenFile(opts.output, flags, 0o600)
			if err != nil {
				if errors.Is(err, os.ErrExist) {
					return fmt.Errorf("%s already exists (use --force to overwrite)", opts.output)
				}
				return fmt.Errorf("create %s: %w", opts.output, err)
			}
			if err = file.Chmod(0o600); err != nil {
				_ = file.Close()
				return fmt.Errorf("secure %s: %w", opts.output, err)
			}
			if _, err = file.Write(data); err != nil {
				_ = file.Close()
				return fmt.Errorf("write %s: %w", opts.output, err)
			}
			if err = file.Close(); err != nil {
				return fmt.Errorf("close %s: %w", opts.output, err)
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
	flags.StringVar(&opts.id, "id", "", "Unique node ID (required)")
	flags.Uint16Var(&opts.port, "port", 57175, "UDP port Nylon listens on")
	flags.StringVar(&opts.key, "key", "", "Existing private key (a new key is generated if omitted)")
	flags.StringVarP(&opts.output, "output", "o", DefaultNodeConfigPath, "Node config output path")
	flags.BoolVar(&opts.force, "force", false, "Overwrite the output file if it exists")
	flags.BoolVar(&opts.useSystemRouting, "use-system-routing", false, "Route peer packets through the system")
	flags.BoolVar(&opts.noNetConfigure, "no-net-configure", false, "Do not configure system networking")
	flags.StringSliceVar(&opts.dnsResolvers, "dns-resolver", nil, "DNS resolver in ip:port form (repeatable)")
	flags.StringVar(&opts.interfaceName, "interface-name", "", "Nylon interface name")
	flags.StringVar(&opts.logPath, "log-path", "", "Log file path")
	flags.StringVar(&opts.distURL, "dist-url", "", "Configuration distribution URL")
	flags.StringVar(&opts.distKey, "dist-key", "", "Configuration distribution public key")
	flags.StringSliceVar(&opts.unexcludeIPs, "unexclude-ip", nil, "Centrally excluded IP prefix to include (repeatable)")
	flags.StringSliceVar(&opts.excludeIPs, "exclude-ip", nil, "IP prefix to exclude (repeatable)")
	flags.StringSliceVar(&opts.preUp, "pre-up", nil, "Command to run before interface startup (repeatable)")
	flags.StringSliceVar(&opts.preDown, "pre-down", nil, "Command to run before interface shutdown (repeatable)")
	flags.StringSliceVar(&opts.postUp, "post-up", nil, "Command to run after interface startup (repeatable)")
	flags.StringSliceVar(&opts.postDown, "post-down", nil, "Command to run after interface shutdown (repeatable)")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

func buildNodeConfig(opts initOptions) (*state.LocalCfg, error) {
	privateKey := state.GenerateKey()
	if opts.key != "" {
		if err := privateKey.UnmarshalText([]byte(opts.key)); err != nil {
			return nil, fmt.Errorf("invalid private key: %w", err)
		}
	}

	cfg := &state.LocalCfg{
		Key:              privateKey,
		Id:               state.NodeId(opts.id),
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

	if (opts.distURL == "") != (opts.distKey == "") {
		return nil, errors.New("--dist-url and --dist-key must be provided together")
	}
	if opts.distURL != "" {
		var key state.NyPublicKey
		if err := key.UnmarshalText([]byte(opts.distKey)); err != nil {
			return nil, fmt.Errorf("invalid distribution key: %w", err)
		}
		cfg.Dist = &state.LocalDistributionCfg{Url: opts.distURL, Key: key}
	}

	if err := state.NodeConfigValidator(nil, cfg); err != nil {
		return nil, fmt.Errorf("invalid node config: %w", err)
	}
	return cfg, nil
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
