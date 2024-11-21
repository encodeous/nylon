use educe::Educe;
use root::framework::{MACSignature, RootData, RoutingSystem};
use serde::{Deserialize, Serialize};
use crate::core::crypto::sig::SignedClaim;

#[derive(Educe)]
#[educe(Clone(bound()))]
#[derive(Serialize, Deserialize)]
#[serde(bound = "")]
pub struct NodeMAC<V: RootData> {
    pub data: V,
}

impl <V: RootData, T: RoutingSystem + ?Sized> MACSignature<V, T> for SignedClaim<V> {
    fn data(&self) -> &V {
        &self.claim.data
    }

    fn data_mut(&mut self) -> &mut V {
        &mut self.claim.data
    }
}

pub struct NylonMAC {
    
}