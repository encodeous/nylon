package impl

import (
	"encoding/hex"
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/nylon/sys"
	"github.com/encodeous/polyamide/conn"
	"github.com/encodeous/polyamide/device"
	"github.com/encodeous/polyamide/ipc"
	"github.com/encodeous/polyamide/tun"
	"net/netip"
	"runtime"
	"slices"
	"sort"
	"strings"
)

func (n *Nylon) initWireGuard(s *state.State) error {
	itfName := "nylon"

	if runtime.GOOS == "darwin" {
		itfName = "utun"
	}

	err := sys.VerifyPreconditions()
	if err != nil {
		return err
	}

	// setup TUN
	tdev, err := tun.CreateTUN(itfName, device.DefaultMTU)
	if err != nil {
		return fmt.Errorf("failed to create TUN: %v. Check if an interface with the name nylon exists already", err)
	}
	realInterfaceName, err := tdev.Name()
	if err == nil {
		itfName = realInterfaceName
	}

	// setup WireGuard
	dev := device.NewDevice(tdev, conn.NewStdNetBind(), &device.Logger{
		Verbosef: func(format string, args ...any) {
			if state.DBG_log_wireguard {
				s.Log.Debug(fmt.Sprintf(format, args...))
			}
		},
		Errorf: func(format string, args ...any) {
			if strings.Contains(format, "Failed to send PolySock packets") {
				return
			}
			s.Log.Error(fmt.Sprintf(format, args...))
		},
	})

	n.Device = dev
	n.itfName = itfName

	err = dev.IpcSet(
		fmt.Sprintf(
			`private_key=%s
listen_port=%d
allow_inbound=true
`,
			hex.EncodeToString(s.Key[:]),
			s.Port,
		),
	)
	if err != nil {
		return fmt.Errorf("failed to configure wg device: %v", err)
	}

	// add peers
	for _, neigh := range s.GetPeers() {
		s.Log.Debug("", "neigh", neigh)
		ncfg := s.MustGetNode(neigh)
		peer, err := dev.NewPeer(device.NoisePublicKey(ncfg.PubKey))
		if err != nil {
			return err
		}
		peer.Start()
		endpoints := make([]conn.Endpoint, 0)
		for _, nep := range ncfg.Endpoints {
			endpoints = append(endpoints, &conn.StdNetEndpoint{AddrPort: nep})
		}
		peer.SetEndpoints(endpoints)
	}

	// start uapi for wg command

	fileUAPI, err := ipc.UAPIOpen(itfName)

	uapi, err := ipc.UAPIListen(itfName, fileUAPI)
	if err != nil {
		return err
	}

	go func() {
		for {
			accept, err := uapi.Accept()
			if err != nil {
				s.Env.Log.Error(err.Error())
			}
			go dev.IpcHandle(accept)
		}
	}()

	s.Log.Info("Created WireGuard interface", "name", itfName)

	// configure system networking

	selfPrefixes := s.MustGetNode(s.Id).Prefixes

	err = sys.InitInterface(itfName)
	if err != nil {
		return err
	}

	if len(selfPrefixes) != 0 {
		// configure system
		// assign ip
		for _, prefix := range selfPrefixes {
			err = sys.ConfigureAlias(itfName, prefix)
			if err != nil {
				return err
			}
		}

		for _, peer := range s.CentralCfg.Nodes {
			if peer.Id == s.Id {
				continue
			}
			for _, prefix := range peer.Prefixes {
				err = sys.ConfigureRoute(itfName, prefix, selfPrefixes[0].Addr())
				if err != nil {
					return err
				}
			}
		}
	}

	// init wireguard related tasks

	n.PolySock = dev.PolyListen(n)
	s.Log.Info("started polysock listener")

	s.RepeatTask(UpdateWireGuard, state.ProbeDpDelay)

	return nil
}

func (n *Nylon) cleanupWireGuard(s *state.State) error {
	n.Device.Close()
	return nil
}

func UpdateWireGuard(s *state.State) error {
	r := Get[*Router](s)
	n := Get[*Nylon](s)
	dev := n.Device

	routesToNeigh := make(map[state.Node][]*state.Route)
	for _, route := range r.Routes {
		routesToNeigh[route.Nh] = append(routesToNeigh[route.Nh], route)
	}

	// configure peers
	for neigh, routes := range routesToNeigh {
		pcfg := s.MustGetNode(neigh)
		allowedIps := make([]string, 0)
		for _, route := range routes {
			cfg, err := s.GetPubNodeCfg(route.Src.Id)
			if err != nil {
				continue
			}
			for _, prefix := range cfg.Prefixes {
				allowedIps = append(allowedIps, prefix.String())
			}
		}
		sort.Strings(allowedIps)

		peer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
		for _, allowedIp := range allowedIps {
			dev.Allowedips.Insert(netip.MustParsePrefix(allowedIp), peer)
		}
	}

	for _, neigh := range s.GetPeers() {
		pcfg := s.MustGetNode(neigh)
		nhNeigh := s.GetNeighbour(neigh)
		eps := make([]conn.Endpoint, 0)

		if nhNeigh != nil {
			links := slices.Clone(nhNeigh.Eps)
			slices.SortStableFunc(links, func(a, b *state.DynamicEndpoint) int {
				return -int(a.Metric() - b.Metric())
			})
			for _, ep := range links {
				eps = append(eps, ep.NetworkEndpoint().GetWgEndpoint())
			}
		}

		// add endpoint if it is not in the list
		for _, ep := range pcfg.Endpoints {
			if !slices.ContainsFunc(eps, func(endpoint conn.Endpoint) bool {
				return endpoint.DstIPPort() == ep
			}) {
				eps = append(eps, &conn.StdNetEndpoint{AddrPort: ep})
			}
		}

		peer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
		peer.SetEndpoints(eps)
	}
	return nil
}
