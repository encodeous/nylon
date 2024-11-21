use std::any::Any;
use chrono::{DateTime, Utc};
use aws_lc_rs::rand::{SecureRandom, SystemRandom};
use aws_lc_rs::signature::{ECDSA_P256_SHA256_ASN1, UnparsedPublicKey};
use serde::{Deserialize, Serialize};
use thiserror::Error;
use uuid::Uuid;
use anyhow::Result;
use crate::core::crypto::entity::{Entity, EntitySecret};

#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct Claim<T: Clone> {
    pub data: T,
    pub not_before: DateTime<Utc>,
    pub not_after: DateTime<Utc>,
    pub serial: Uuid,
}

#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct SignedClaim<T: Clone> {
    pub claim: Claim<T>,
    #[serde(with = "hex::serde")]
    pub signature: Vec<u8>
}

impl<T: Clone + Serialize + Any> Claim<T>{
    pub fn to_serialized(&self) -> Result<Vec<u8>>{
        Ok(serde_json::to_vec(&self)?)
    }
    pub fn from(data: T, not_before: DateTime<Utc>, not_after: DateTime<Utc>) -> Claim<T> {
        let mut uuid_bytes: [u8; 16] = [0; 16];
        let rng = SystemRandom::new();
        rng.fill(&mut uuid_bytes).expect("Unable to generate random bytes");

        Claim {
            data,
            not_before,
            not_after,
            serial: Uuid::from_bytes(uuid_bytes)
        }
    }

    pub fn from_now_forever(data: T) -> Claim<T>{
        Self::from(data, Utc::now(), DateTime::<Utc>::MAX_UTC)
    }

    pub fn from_now_until(data: T, end: DateTime<Utc>) -> Claim<T>{
        Self::from(data, Utc::now(), end)
    }

    pub fn sign_claim(&self, key_pair: &EntitySecret) -> Result<SignedClaim<T>> {
        Ok(SignedClaim {
            claim: self.clone(),
            signature: key_pair.sign(&self.to_serialized()?)?
        })
    }
}

impl<T: Clone + Serialize + Any> SignedClaim<T>{
    pub fn validate(&self, entity: &Entity) -> Result<(), ValidationError> {
        let cur_time = Utc::now();
        if cur_time >= self.claim.not_after || cur_time < self.claim.not_before {
            return Err(ValidationError::Inactive);
        }

        // validate signature
        let sig = &self.signature;
        let pub_key = UnparsedPublicKey::new(&ECDSA_P256_SHA256_ASN1, &entity.pub_key);
        let valid = pub_key.verify(&self.claim.to_serialized().map_err(|e| {
            ValidationError::SerializationError(e)
        })?, sig);

        if valid.is_err(){
            return Err(ValidationError::InvalidSignature);
        }
        Ok(())
    }
}

#[derive(Error, Debug)]
pub enum ValidationError {
    #[error("Signature error, unable to serialize claim `{0}`")]
    SerializationError(anyhow::Error),
    /// The certificate is either expired or not active yet
    #[error("The certificate is either expired or not active yet")]
    Inactive,
    #[error("The signature is invalid")]
    InvalidSignature,
}