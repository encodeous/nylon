use std::any::Any;
use std::fmt::{Display, Formatter};
use aws_lc_rs::error::KeyRejected;
use aws_lc_rs::signature::{EcdsaKeyPair, KeyPair, ECDSA_P256_SHA256_ASN1_SIGNING};
use serde::{Deserialize, Serialize};
use anyhow::Result;
use aws_lc_rs::rand::SystemRandom;
use base64::engine::general_purpose::STANDARD;
use base64_serde::base64_serde_type;
use chrono::{DateTime, Utc};
use zeroize::Zeroize;
use crate::core::crypto::sig::{Claim, SignedClaim};

base64_serde_type!(Base64Standard, STANDARD);

#[derive(Serialize, Deserialize, Clone, Eq, PartialEq, Hash, Debug)]
#[serde(transparent)]
pub struct Entity {
    #[serde(with = "Base64Standard")]
    pub pub_key: Vec<u8>,
}

impl Display for Entity{
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        f.write_str(hex::encode(&self.pub_key).as_str())
    }
}

#[derive(Serialize, Deserialize, Clone, Eq, PartialEq, Hash, Debug, Zeroize)]
#[serde(transparent)]
pub struct EntitySecret {
    #[serde(with = "Base64Standard")]
    pub pkcs8: Vec<u8>,
}

impl EntitySecret {
    pub fn to_keypair(&self) -> EcdsaKeyPair {
        EcdsaKeyPair::from_pkcs8(&ECDSA_P256_SHA256_ASN1_SIGNING, self.pkcs8.as_slice()).unwrap()
    }
    pub fn get_pubkey(&self) -> Entity {
        Entity::from_secret(self)
    }

    pub fn sign(&self, data: &[u8]) -> Result<Vec<u8>> {
        let rand = SystemRandom::new();
        let res = self.to_keypair().sign(&rand, data)?;
        Ok(res.as_ref().to_vec())
    }
    pub fn generate() -> EntitySecret {
        let kp = EcdsaKeyPair::generate(&ECDSA_P256_SHA256_ASN1_SIGNING)
            .expect("System Error: Unable to generate ECDSA Keypair");
        EntitySecret { pkcs8: kp.to_pkcs8v1().unwrap().as_ref().to_vec() }
    }

    pub fn sign_forever<T: Clone + Serialize + 'static>(&self, data: T) -> Result<SignedClaim<T>> {
        Claim::<T>::from_now_forever(data).sign_claim(&self)
    }

    pub fn sign_until<T: Clone + Serialize + 'static>(&self, data: T, end: DateTime<Utc>) -> Result<SignedClaim<T>> {
        Claim::<T>::from_now_until(data, end).sign_claim(&self)
    }
}

impl Entity {
    pub fn from_pkcs8(pkcs8: &Vec<u8>) -> Result<(Entity, EntitySecret), KeyRejected> {
        let secret = EntitySecret { pkcs8: pkcs8.clone() };
        let kp = EcdsaKeyPair::from_pkcs8(&ECDSA_P256_SHA256_ASN1_SIGNING, pkcs8.as_ref())?;
        
        Ok((Self::from_keypair(&kp), secret))
    }
    
    pub fn from_keypair(key_pair: &EcdsaKeyPair) -> Entity {
        Entity {
            pub_key: key_pair.public_key().as_ref().to_vec(),
        }
    }
    pub fn from_secret(key_pair: &EntitySecret) -> Entity {
        Entity {
            pub_key: key_pair.to_keypair().public_key().as_ref().to_vec(),
        }
    }
}
