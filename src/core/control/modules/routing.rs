use log::info;
use root::concepts::packet::Packet;
use root::framework::RoutingSystem;
use root::router::DummyMAC;
use serde_json::json;
use crate::core::routing::NylonSystem;
use crate::core::structure::network::{NetPacket, UnifiedAddr};
use crate::core::structure::state::NylonEvent::NoEvent;
use crate::core::structure::state::NylonState;

pub fn handle_routing_packet(
    state: &mut NylonState,
    pkt: Packet<NylonSystem>,
    src: UnifiedAddr
) -> anyhow::Result<()> {
    let link = state.get_link_uni(&src)?.clone();
    let NylonState{ps, os, mq, ..} = state;
    if os.log_routing {
        info!("RP From: {}, {}, via {}", link.addr_vlan, json!(pkt), link.id);
    }
    ps.router.handle_packet(&DummyMAC::from(pkt), &link.id, &link.addr_vlan)?;
    write_routing_packets(state)?;
    Ok(())
}


pub fn timed_routing_update(state: &mut NylonState) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    ps.router.full_update();
    write_routing_packets(state)?;
    Ok(())
}

fn write_routing_packets(state: &mut NylonState) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let mq = mq.clone();
    let node = &os.node_config;
    for pkt in ps.router.outbound_packets.drain(..){
        if let Some(peer) = node.get_link(&pkt.link){
            mq.send_packet(
                peer.id.clone(),
                NetPacket::Routing(pkt.packet.data.clone()),
                NoEvent
            );
        }
    }
    Ok(())
}