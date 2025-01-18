//go:generate protoc -I . --go_out=. ./core/proto/nylon.proto
package main

import "github.com/encodeous/nylon/core"

func main() {
	err := core.Start()
	if err != nil {
		panic(err)
	}

	//ctl := make(chan network.TCPCtlLink)
	//go func() {
	//	err := network.ListenCtlTCP("0.0.0.0:8082", ctl)
	//	if err != nil {
	//		panic(err)
	//	}
	//}()
	//
	//go func() {
	//	for conn := range ctl {
	//		var pkt network.CtlMsg
	//		err := conn.ReceivePacket(&pkt)
	//		if err != nil {
	//			slog.Warn(err.Error())
	//			return
	//		}
	//		switch pkt.Type.(type) {
	//		case *network.CtlMsg_RouteUpdate:
	//			slog.Info("route update", "pkt", pkt.GetRouteUpdate())
	//		case *network.CtlMsg_Seqno:
	//
	//			slog.Info("seqno request", "pkt", pkt.GetSeqno())
	//		}
	//	}
	//}()
	//
	//time.Sleep(1 * time.Second)
	//
	//client, err := network.ConnectCtlTCP("127.0.0.1:8082")
	//if err != nil {
	//	panic(err)
	//}
	//
	//err = client.SendPacket(&network.CtlMsg{Type: &network.CtlMsg_Seqno{Seqno: &network.CtlSeqnoRequest{Current: &network.Source{
	//	Seqno: 1,
	//	Id:    "aaa",
	//	Sig:   make([]byte, 2),
	//},
	//}}})
	//if err != nil {
	//	panic(err)
	//}
	//
	//time.Sleep(10 * time.Second)

}
