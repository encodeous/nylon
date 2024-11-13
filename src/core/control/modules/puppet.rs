use std::net::IpAddr;
use std::time::Instant;
use anyhow::anyhow;
use log::{error, info};
use crate::core::control::modules::courier::{route_packet, RoutedPacket};
use crate::core::control::modules::courier::CourierPacket::TraceRoute;
use crate::core::structure::network::CtlPacket::PCourier;
use crate::core::structure::state::{MessageQueue, NylonState};
use crate::core::structure::state::NylonEvent::{NoEvent, Shutdown};

// fn handle_command(
//     state: &mut NylonState,
//     cmd: String,
//     mq: MessageQueue,
// ) -> anyhow::Result<()> {
//     let NylonState{os, mq, ps} = state;
//     let mq = mq.clone();
//     let split: Vec<&str> = cmd.split_whitespace().collect();
//     if split.is_empty() {
//         return Ok(());
//     }
//     let peers = &os.node_config;
//     match split[0] {
//         "help" => {
//             info!(r#"Help:
//                 - help -- shows this page
//                 - exit -- exits and saves state
//                 [direct link]
//                 - ls -- lists all direct links
//                 [core]
//                 - route -- prints whole route table
//                 - ping <node-name> -- pings node
//                 - msg <node-name> <message> -- sends a message to a node
//                 - traceroute/tr <node-name> -- traces a route to a node
//                 [debug]
//                 - rpkt -- log core protocol control packets
//                 - dpkt -- log core/forwarded packets
//                 "#);
//         }
//         "route" => {
//             let mut rtable = vec![];
//             info!("Route Table:");
//             rtable.push(String::new());
//             rtable.push(format!("Self: {}, seq: {}", ps.router.address, ps.router.seqno));
//             for (addr, route) in &ps.router.routes {
//                 rtable.push(
//                     format!("{addr} - via: {}, nh: {}, c: {}, seq: {}, fd: {}, ret: {}",
//                             route.link,
//                             route.next_hop.clone(),
//                             route.metric,
//                             route.source.data.seqno,
//                             route.fd,
//                             route.retracted
//                     ))
//             }
//             info!("{}", rtable.join("\n"));
//         }
//         "rpkt" => {
//             os.log_routing = !os.log_routing;
//         }
//         "dpkt" => {
//             os.log_delivery = !os.log_delivery;
//         }
//         "exit" => {
//             mq.main.send(Shutdown)?;
//         }
//         "ping" => {
//             if split.len() != 2 {
//                 return Err(anyhow!("Expected one argument"));
//             }
//             let node = split[1];
//             os.pings.insert(node.parse()?, Instant::now());
//             let addr = ps.router.address.clone();
//             route_packet(state, RoutedPacket::Ping, node.parse()?, addr)?;
//         }
//         "traceroute" | "tr" => {
//             if split.len() != 2 {
//                 return Err(anyhow!("Expected one argument"));
//             }
//             let node: IpAddr = split[1].to_string().parse()?;
//             if let Some(nh) = ps.router.routes.get(&node) {
//                 if let Some(peer) = peers.get_link(&nh.link) {
//                     let s_addr = ps.router.address.clone();
//                     mq.send_packet(
//                         peer.id.clone(),
//                         PCourier(
//                             TraceRoute {
//                                 path: vec![],
//                                 dst_id: node.clone(),
//                                 sender_id: s_addr,
//                             }
//                         ),
//                         NoEvent
//                     );
//                 }
//             }
//         }
//         "msg" => {
//             if split.len() <= 2 {
//                 return Err(anyhow!("Expected at least two arguments"));
//             }
//             let node: IpAddr = split[1].to_string().parse()?;
//             let msg = split[2..].join(" ");
//             let addr = ps.router.address.clone();
//             route_packet(state, RoutedPacket::Message(msg), node, addr)?;
//         }
//         "ls" => {
//             for LinkConfig { id, addr_ctl, addr_vlan, .. } in &peers.links {
//                 if let Some(health) = os.health.get(id) {
//                     info!("id: {id}, ctl: {}, vlan: {}, ping: {:?}", addr_ctl, addr_vlan, health.ping)
//                 } else {
//                     info!("id: {id}, ctl: {} UNCONNECTED", addr_ctl)
//                 }
//             }
//         }
//         &_ => {
//             error!("Unknown command, please try again or type \"help\" for help.")
//         }
//     }
//     Ok(())
// }