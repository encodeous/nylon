package device

import "time"

type PeerStatus struct {
	PublicKey                   NoisePublicKey
	Endpoint                    string
	LatestHandshakeUnixNano     int64
	TxBytes                     uint64
	RxBytes                     uint64
	PersistentKeepaliveInterval uint32
}

func (device *Device) ListenPort() uint16 {
	device.net.RLock()
	defer device.net.RUnlock()
	return device.net.port
}

func (peer *Peer) Status() PeerStatus {
	endpoint := ""
	peer.endpoints.Lock()
	if len(peer.endpoints.val) > 0 {
		endpoint = peer.endpoints.val[0].DstToString()
	}
	peer.endpoints.Unlock()

	peer.handshake.mutex.RLock()
	publicKey := peer.handshake.remoteStatic
	peer.handshake.mutex.RUnlock()

	return PeerStatus{
		PublicKey:                   publicKey,
		Endpoint:                    endpoint,
		LatestHandshakeUnixNano:     peer.lastHandshakeNano.Load(),
		TxBytes:                     peer.txBytes.Load(),
		RxBytes:                     peer.rxBytes.Load(),
		PersistentKeepaliveInterval: peer.persistentKeepaliveInterval.Load(),
	}
}

func (s PeerStatus) LatestHandshakeTime() time.Time {
	if s.LatestHandshakeUnixNano == 0 {
		return time.Time{}
	}
	return time.Unix(0, s.LatestHandshakeUnixNano)
}
