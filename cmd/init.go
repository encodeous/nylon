package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
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
			if opts.id == "" {
				if err := promptInitOptions(cmd, &opts); err != nil {
					return err
				}
			}

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
	flags.StringVar(&opts.distKey, "dist-key", "", "Configuration distribution shared key")
	flags.StringSliceVar(&opts.unexcludeIPs, "unexclude-ip", nil, "Centrally excluded IP prefix to include (repeatable)")
	flags.StringSliceVar(&opts.excludeIPs, "exclude-ip", nil, "IP prefix to exclude (repeatable)")
	flags.StringSliceVar(&opts.preUp, "pre-up", nil, "Command to run before interface startup (repeatable)")
	flags.StringSliceVar(&opts.preDown, "pre-down", nil, "Command to run before interface shutdown (repeatable)")
	flags.StringSliceVar(&opts.postUp, "post-up", nil, "Command to run after interface startup (repeatable)")
	flags.StringSliceVar(&opts.postDown, "post-down", nil, "Command to run after interface shutdown (repeatable)")
	return cmd
}

type initPrompter struct {
	scanner *bufio.Scanner
	output  io.Writer
}

func promptInitOptions(cmd *cobra.Command, opts *initOptions) error {
	prompter := initPrompter{
		scanner: bufio.NewScanner(cmd.InOrStdin()),
		output:  cmd.OutOrStdout(),
	}

	fmt.Fprintln(prompter.output, "Interactive node configuration (press Enter to accept a default)")

	for {
		id, err := prompter.stringValue("Node ID", opts.id)
		if err != nil {
			return err
		}
		if id == "" {
			fmt.Fprintln(prompter.output, "Node ID is required.")
			continue
		}
		if err := state.NameValidator(id); err != nil {
			fmt.Fprintf(prompter.output, "Invalid node ID: %v\n", err)
			continue
		}
		opts.id = id
		break
	}

	port, err := prompter.portValue("UDP port", opts.port)
	if err != nil {
		return err
	}
	opts.port = port

	output, err := prompter.stringValue("Output path", opts.output)
	if err != nil {
		return err
	}
	opts.output = output
	if _, err := os.Stat(opts.output); err == nil {
		overwrite, promptErr := prompter.boolValue("Output already exists; overwrite it", opts.force)
		if promptErr != nil {
			return promptErr
		}
		if !overwrite {
			return fmt.Errorf("%s already exists (use --force to overwrite)", opts.output)
		}
		opts.force = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", opts.output, err)
	}

	key, err := prompter.stringValue("Existing private key (leave blank to generate one)", opts.key)
	if err != nil {
		return err
	}
	opts.key = key

	advanced, err := prompter.boolValue("Configure advanced options", hasAdvancedInitOptions(*opts))
	if err != nil {
		return err
	}
	if !advanced {
		return nil
	}

	if opts.useSystemRouting, err = prompter.boolValue("Use system routing", opts.useSystemRouting); err != nil {
		return err
	}
	if opts.noNetConfigure, err = prompter.boolValue("Disable automatic network configuration", opts.noNetConfigure); err != nil {
		return err
	}
	if opts.dnsResolvers, err = prompter.listValue("DNS resolvers (comma-separated ip:port values)", opts.dnsResolvers); err != nil {
		return err
	}
	if opts.interfaceName, err = prompter.stringValue("Interface name", opts.interfaceName); err != nil {
		return err
	}
	if opts.logPath, err = prompter.stringValue("Log path", opts.logPath); err != nil {
		return err
	}

	distribution, err := prompter.boolValue("Configure remote configuration distribution", opts.distURL != "" || opts.distKey != "")
	if err != nil {
		return err
	}
	if distribution {
		if opts.distURL, err = prompter.stringValue("Distribution URL", opts.distURL); err != nil {
			return err
		}
		if opts.distKey, err = prompter.stringValue("Distribution shared key", opts.distKey); err != nil {
			return err
		}
	} else {
		opts.distURL = ""
		opts.distKey = ""
	}

	if opts.unexcludeIPs, err = prompter.listValue("IP prefixes to unexclude (comma-separated)", opts.unexcludeIPs); err != nil {
		return err
	}
	if opts.excludeIPs, err = prompter.listValue("IP prefixes to exclude (comma-separated)", opts.excludeIPs); err != nil {
		return err
	}
	if opts.preUp, err = prompter.listValue("Pre-up commands (comma-separated)", opts.preUp); err != nil {
		return err
	}
	if opts.preDown, err = prompter.listValue("Pre-down commands (comma-separated)", opts.preDown); err != nil {
		return err
	}
	if opts.postUp, err = prompter.listValue("Post-up commands (comma-separated)", opts.postUp); err != nil {
		return err
	}
	if opts.postDown, err = prompter.listValue("Post-down commands (comma-separated)", opts.postDown); err != nil {
		return err
	}
	return nil
}

func hasAdvancedInitOptions(opts initOptions) bool {
	return opts.useSystemRouting || opts.noNetConfigure || len(opts.dnsResolvers) > 0 ||
		opts.interfaceName != "" || opts.logPath != "" || opts.distURL != "" || opts.distKey != "" ||
		len(opts.unexcludeIPs) > 0 || len(opts.excludeIPs) > 0 || len(opts.preUp) > 0 ||
		len(opts.preDown) > 0 || len(opts.postUp) > 0 || len(opts.postDown) > 0
}

func (p *initPrompter) readLine(prompt string) (string, error) {
	fmt.Fprint(p.output, prompt)
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			return "", fmt.Errorf("read interactive input: %w", err)
		}
		return "", errors.New("interactive input ended before configuration was complete")
	}
	return strings.TrimSpace(p.scanner.Text()), nil
}

func (p *initPrompter) stringValue(label, current string) (string, error) {
	prompt := label + ": "
	if current != "" {
		prompt = fmt.Sprintf("%s [%s]: ", label, current)
	}
	value, err := p.readLine(prompt)
	if err != nil {
		return "", err
	}
	if value == "" {
		return current, nil
	}
	return value, nil
}

func (p *initPrompter) portValue(label string, current uint16) (uint16, error) {
	for {
		value, err := p.readLine(fmt.Sprintf("%s [%d]: ", label, current))
		if err != nil {
			return 0, err
		}
		if value == "" {
			return current, nil
		}
		parsed, parseErr := strconv.ParseUint(value, 10, 16)
		if parseErr == nil && parsed > 0 {
			return uint16(parsed), nil
		}
		fmt.Fprintln(p.output, "Port must be a number between 1 and 65535.")
	}
}

func (p *initPrompter) boolValue(label string, current bool) (bool, error) {
	defaultHint := "y/N"
	if current {
		defaultHint = "Y/n"
	}
	for {
		value, err := p.readLine(fmt.Sprintf("%s? [%s]: ", label, defaultHint))
		if err != nil {
			return false, err
		}
		switch strings.ToLower(value) {
		case "":
			return current, nil
		case "y", "yes", "true":
			return true, nil
		case "n", "no", "false":
			return false, nil
		default:
			fmt.Fprintln(p.output, "Please answer yes or no.")
		}
	}
}

func (p *initPrompter) listValue(label string, current []string) ([]string, error) {
	prompt := label + ": "
	if len(current) > 0 {
		prompt = fmt.Sprintf("%s [%s]: ", label, strings.Join(current, ", "))
	}
	value, err := p.readLine(prompt)
	if err != nil {
		return nil, err
	}
	if value == "" {
		return current, nil
	}
	values := strings.Split(value, ",")
	result := make([]string, 0, len(values))
	for _, item := range values {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result, nil
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
