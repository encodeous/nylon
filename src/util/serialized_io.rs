use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncReadExt, AsyncWrite, AsyncWriteExt};

pub async fn read_data<T: DeserializeOwned>(sock: &mut (impl AsyncReadExt + std::marker::Unpin), buf: &mut Vec<u8>) -> anyhow::Result<T>{
    let len = sock.read_u32().await? as usize;
    if len > 256000 {
        anyhow::bail!("data too long");
    }
    buf.resize(len, 0);
    sock.read_exact(&mut buf[..len]).await?;
    Ok(serde_json::from_slice(&buf[..len])?)
}

pub async fn write_data<T: Serialize>(sock: &mut (impl AsyncWriteExt + std::marker::Unpin), mut buf: &mut Vec<u8>, data: &T) -> anyhow::Result<()>{
    buf.clear();
    serde_json::to_writer(&mut buf, data)?;
    sock.write_u32(buf.len() as u32).await?;
    sock.write_all(&buf).await?;
    Ok(())
}