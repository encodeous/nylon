package core

// Linux GRO coalesces later packets into the spare capacity of a host buffer,
// so each one can hold a full TUN packet.
const minHostBufferCap = tunHeadroom + maxTUNPacketSize
