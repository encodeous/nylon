package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

const (
	joinPath        = "/nylon/join"
	maxJoinBodySize = 1 << 20
)

type joinRequest struct {
	Id        state.NodeId
	PubKey    state.NyPublicKey
	Addresses []netip.Addr   `yaml:",omitempty"`
	Prefixes  []netip.Prefix `yaml:",omitempty"`
}

// address and prefix flags describe this node's entry in central config
func entryFlags(opts initOptions) ([]netip.Addr, []netip.Prefix, error) {
	addrs, err := parseAddrs(opts.addresses)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid --address: %w", err)
	}
	prefixes, err := parsePrefixes(opts.prefixes)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid --prefix: %w", err)
	}
	return addrs, prefixes, nil
}

func staticPrefixes(prefixes []netip.Prefix) []state.PrefixHealthWrapper {
	res := make([]state.PrefixHealthWrapper, 0, len(prefixes))
	for _, p := range prefixes {
		res = append(res, state.PrefixHealthWrapper{PrefixHealth: &state.StaticPrefixHealth{Prefix: p}})
	}
	return res
}

// token doubles as the shared secret sealing both directions of the exchange
func tokenKey(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func runInitServe(cmd *cobra.Command, opts initOptions) error {
	out := cmd.OutOrStdout()
	node, err := loadOrInitNode(cmd, opts)
	if err != nil {
		return err
	}
	central, existed, err := loadOrInitCentral(opts, node)
	if err != nil {
		return err
	}
	// an existing network keeps its central config until the join is distributed
	outPath := opts.central
	if existed {
		outPath = joinConfigPath(opts.central)
		if !opts.force {
			if err = checkPendingJoin(outPath, central); err != nil {
				return err
			}
		}
	}

	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	key := tokenKey(token)

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", node.Port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	var mu sync.Mutex
	used := false
	joined := make(chan joinRequest, 1)
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != joinPath {
				http.NotFound(w, r)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxJoinBodySize))
			if err != nil {
				http.Error(w, "read request", http.StatusBadRequest)
				return
			}
			plain, err := state.OpenBundle(body, key)
			if err != nil {
				http.Error(w, "invalid setup token", http.StatusForbidden)
				return
			}
			var req joinRequest
			if err = yaml.Unmarshal(plain, &req); err != nil {
				http.Error(w, "invalid join request", http.StatusBadRequest)
				return
			}

			mu.Lock()
			defer mu.Unlock()
			if used {
				http.Error(w, "setup token already used", http.StatusForbidden)
				return
			}
			updated, err := enrolNode(central, node, req, r.Host)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data, err := yaml.Marshal(updated)
			if err == nil {
				err = os.WriteFile(outPath, data, 0o600)
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Error: write %s: %v\n", outPath, err)
				http.Error(w, "failed to update central config", http.StatusInternalServerError)
				return
			}
			sealed, err := state.SealBundle(data, key)
			if err != nil {
				http.Error(w, "failed to seal central config", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(sealed)
			used = true
			central = updated
			joined <- req
		}),
	}
	go func() { _ = srv.Serve(ln) }()

	fmt.Fprintf(out, "Setup token: %s\n\nOn the new node, run:\n\n  nylon init --connect <this host> --token %s\n\nWaiting for a node to join on tcp port %d...\n", token, token, node.Port)
	req := <-joined
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)

	fmt.Fprintf(out, "\nNode %s joined with address %v, wrote %s\nIt has no connections yet, add it to graph (e.g. `%s, %s`) then:\n",
		req.Id, central.GetNode(req.Id).Addresses, outPath, node.Id, req.Id)
	switch {
	case !existed:
		fmt.Fprintln(out, "Start nylon")
	case central.Dist != nil:
		fmt.Fprintf(out, "Seal it and publish it to %s:\n\n  nylon seal -c %s -k %s -o %s\n",
			strings.Join(central.Dist.Repos, ", "), outPath, DefaultKeyPath, DefaultBundlePath)
	default:
		fmt.Fprintf(out, "Copy it to every node (including this one) as %s and run `nylon reload`\n", filepath.Base(opts.central))
	}
	return nil
}

// reuse this node's config when present so --serve works on an existing network
func loadOrInitNode(cmd *cobra.Command, opts initOptions) (*state.LocalCfg, error) {
	data, err := os.ReadFile(opts.output)
	if err == nil {
		cfg, err := state.ParseLocalConfig(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", opts.output, err)
		}
		if err = state.NodeConfigValidator(nil, cfg); err != nil {
			return nil, fmt.Errorf("invalid node config: %w", err)
		}
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", opts.output, err)
	}
	cfg, err := buildNodeConfig(opts)
	if err != nil {
		return nil, err
	}
	if err = writeNodeConfig(cfg, opts.output, false); err != nil {
		return nil, err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", opts.output)
	return cfg, nil
}

func loadOrInitCentral(opts initOptions, node *state.LocalCfg) (*state.CentralCfg, bool, error) {
	path := opts.central
	cfg := &state.CentralCfg{}
	data, err := os.ReadFile(path)
	existed := err == nil
	if existed {
		if err = yaml.Unmarshal(data, cfg); err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if cfg.TryGetNode(node.Id) == nil {
		addrs, prefixes, err := entryFlags(opts)
		if err != nil {
			return nil, false, err
		}
		cfg.Routers = append(cfg.Routers, state.RouterCfg{NodeCfg: state.NodeCfg{
			Id:        node.Id,
			PubKey:    node.Key.Pubkey(),
			Addresses: addrs,
			Prefixes:  staticPrefixes(prefixes),
		}})
	}
	if err = state.CentralConfigValidator(cfg); err != nil {
		return nil, false, fmt.Errorf("invalid central config: %w", err)
	}
	if err = state.NodeConfigValidator(cfg, node); err != nil {
		return nil, false, fmt.Errorf("invalid node config: %w", err)
	}
	return cfg, existed, nil
}

func joinConfigPath(central string) string {
	ext := filepath.Ext(central)
	return strings.TrimSuffix(central, ext) + ".join" + ext
}

// a previous join is distributed once all its nodes reach central config
func checkPendingJoin(path string, central *state.CentralCfg) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var pending state.CentralCfg
	if err = yaml.Unmarshal(data, &pending); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for _, n := range pending.GetNodes() {
		if !central.IsNode(n.Id) {
			return fmt.Errorf("%s has an undistributed join for %s, distribute it first (or use --force to discard it)", path, n.Id)
		}
	}
	return nil
}

func enrolNode(central *state.CentralCfg, self *state.LocalCfg, req joinRequest, host string) (*state.CentralCfg, error) {
	err, cfg := central.Clone()
	if err != nil {
		return nil, err
	}
	if err = state.NameValidator(string(req.Id)); err != nil {
		return nil, err
	}
	if cfg.IsNode(req.Id) {
		return nil, fmt.Errorf("node %s already exists, pick another --id", req.Id)
	}
	if cfg.FindNodeBy(req.PubKey) != nil {
		return nil, fmt.Errorf("public key is already in use")
	}
	for _, n := range cfg.GetNodes() {
		for _, a := range n.Addresses {
			if slices.Contains(req.Addresses, a) {
				return nil, fmt.Errorf("address %s is already used by %s", a, n.Id)
			}
		}
	}
	cfg.Routers = append(cfg.Routers, state.RouterCfg{NodeCfg: state.NodeCfg{
		Id:        req.Id,
		PubKey:    req.PubKey,
		Addresses: req.Addresses,
		Prefixes:  staticPrefixes(req.Prefixes),
	}})

	// the joining node reached us through host so it is a usable endpoint
	idx := slices.IndexFunc(cfg.Routers, func(r state.RouterCfg) bool { return r.Id == self.Id })
	if idx != -1 && len(cfg.Routers[idx].Endpoints) == 0 {
		if h, _, err := net.SplitHostPort(host); err == nil {
			cfg.Routers[idx].Endpoints = []string{net.JoinHostPort(h, strconv.Itoa(int(self.Port)))}
		}
	}
	if err = state.CentralConfigValidator(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func runInitConnect(cmd *cobra.Command, opts initOptions) error {
	// fail before enrolling so the server does not register a node we cannot save
	if !opts.force {
		for _, p := range []string{opts.output, opts.central} {
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("%s already exists (use --force to overwrite)", p)
			}
		}
	}
	node, err := buildNodeConfig(opts)
	if err != nil {
		return err
	}
	addrs, prefixes, err := entryFlags(opts)
	if err != nil {
		return err
	}

	key := tokenKey(opts.token)
	plain, err := yaml.Marshal(joinRequest{Id: node.Id, PubKey: node.Key.Pubkey(), Addresses: addrs, Prefixes: prefixes})
	if err != nil {
		return err
	}
	sealed, err := state.SealBundle(plain, key)
	if err != nil {
		return err
	}
	host := opts.connect
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(state.DefaultPort))
	}
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Post("http://"+host+joinPath, "application/octet-stream", bytes.NewReader(sealed))
	if err != nil {
		return fmt.Errorf("connect to %s: %w", host, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxJoinBodySize))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("join rejected: %s", strings.TrimSpace(string(body)))
	}
	data, err := state.OpenBundle(body, key)
	if err != nil {
		return fmt.Errorf("response was not sealed with the setup token: %w", err)
	}
	var central state.CentralCfg
	if err = yaml.Unmarshal(data, &central); err != nil {
		return fmt.Errorf("parse central config: %w", err)
	}
	if err = state.CentralConfigValidator(&central); err != nil {
		return fmt.Errorf("invalid central config: %w", err)
	}
	if err = state.NodeConfigValidator(&central, node); err != nil {
		return fmt.Errorf("invalid node config: %w", err)
	}

	if err = writeNodeConfig(node, opts.output, opts.force); err != nil {
		return err
	}
	if err = writeSecretFile(opts.central, data, opts.force); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Joined as %s with address %v\nCreated %s and %s\nStart nylon with: sudo nylon run -c %s -n %s\n",
		node.Id, central.GetNode(node.Id).Addresses, opts.output, opts.central, opts.central, opts.output)
	return nil
}
