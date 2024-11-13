use root::concepts::packet::Packet;
use root::router::DummyMAC;
use crate::core::routing::{LinkType, NylonSystem};
use crate::core::structure::network::{CtlPacket};
use crate::core::structure::state::NylonEvent::NoEvent;
use crate::core::structure::state::NylonState;

pub fn handle_routing_packet(
    state: &mut NylonState,
    pkt: Packet<NylonSystem>,
    src: LinkType
) -> anyhow::Result<()> {
    let pubkey = state.get_link(&src)?.dst.pubkey.clone();
    state.ps.router.handle_packet(&DummyMAC::from(pkt), &src, &pubkey)?;
    write_routing_packets(state)?;
    Ok(())
}


pub fn timed_routing_update(state: &mut NylonState) -> anyhow::Result<()> {
    let NylonState{ps,   ..} = state;
    ps.router.full_update();
    write_routing_packets(state)?;
    Ok(())
}

fn write_routing_packets(state: &mut NylonState) -> anyhow::Result<()> {
    let mq = state.mq.clone();
    for pkt in &state.ps.router.outbound_packets{
        if let Ok(peer) = state.get_link(&pkt.link){
            mq.send_packet(
                peer.id.clone(),
                CtlPacket::Routing(pkt.packet.data.clone()),
                NoEvent
            );
        }
    }
    state.ps.router.outbound_packets.clear();
    Ok(())
}