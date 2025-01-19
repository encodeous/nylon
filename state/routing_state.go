package state

type Node string

type Neighbour struct {
	Id       Node
	Routes   map[Node]PubRoute
	NodeSrc  map[Node]Source
	DpLinks  []DpLink
	CtlLinks []CtlLink
	Metric   uint16
}

type Route struct {
	PubRoute
	Fd   uint16 // feasibility distance
	Link DpLink
	Nh   Node // next hop node
}

type Source struct {
	Id    Node
	Seqno uint16 // Sequence Number
	Sig   []byte // signature
}

type PubRoute struct {
	Src       Source
	Metric    uint16
	Retracted bool
}
