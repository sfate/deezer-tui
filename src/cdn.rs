#![allow(dead_code)]

use anyhow::{anyhow, Result};
use aes::cipher::{block_padding::NoPadding, BlockEncryptMut, KeyInit};

const CDN_AES_KEY: &[u8; 16] = b"jo6aey6haid2Teih";
const FIELD_SEPARATOR: char = '\u{00A4}';

type Aes128EcbEnc = ecb::Encryptor<aes::Aes128>;

pub fn build_cdn_url(
    md5_origin: &str,
    song_id: &str,
    format: u8,
    media_version: &str,
) -> Result<String> {
    let joined = format!(
        "{md5_origin}{FIELD_SEPARATOR}{format}{FIELD_SEPARATOR}{song_id}{FIELD_SEPARATOR}{media_version}"
    );
    let joined_hash = hex::encode(md5::compute(joined.as_bytes()).0);
    let payload = format!("{joined_hash}{FIELD_SEPARATOR}{joined}{FIELD_SEPARATOR}");
    let mut padded = payload.into_bytes();

    while padded.len() % 16 != 0 {
        padded.push(0);
    }

    let padded_len = padded.len();
    let encrypted = Aes128EcbEnc::new(CDN_AES_KEY.into())
        .encrypt_padded_mut::<NoPadding>(&mut padded, padded_len)
        .map_err(|_| anyhow!("AES-ECB encryption failed for CDN payload"))?
        .to_vec();

    if encrypted.is_empty() {
        return Err(anyhow!("AES-ECB encryption produced an empty CDN payload"));
    }

    let encrypted_hex = hex::encode(encrypted);
    Ok(format!(
        "https://f-cdnt-stream.dzcdn.net/mobile/1/{}",
        encrypted_hex
    ))
}

#[cfg(test)]
mod tests {
    use super::build_cdn_url;

    #[test]
    fn builds_stable_cdn_url() {
        let url = build_cdn_url("0123456789abcdef0123456789abcdef", "3135556", 3, "1")
            .expect("url generation should succeed");

        assert_eq!(
            url,
            "https://f-cdnt-stream.dzcdn.net/mobile/1/5106275779699c6475da81bd1e291387ad490ae2b5d16f20391b487b77a80a89c0efbb341173e62e6c9a982b1267afb619cf52ab2fc1386eb8629b2e9ba88a55055f121500d83c92af111892de3d9edf393b413b3be2ee27eb61e8a66b5c0705"
        );
    }
}
