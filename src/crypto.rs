#![allow(dead_code)]

use std::fmt::Write;

use anyhow::{anyhow, Result};
use blowfish::cipher::{block_padding::NoPadding, BlockDecryptMut, KeyIvInit};
use blowfish::Blowfish;
use cbc::Decryptor as BlowfishCbcDec;

#[cfg(test)]
use cbc::Encryptor as BlowfishCbcEnc;

const DEEZER_SECRET: &str = "g4el58wc0zvf9na1";
const CHUNK_SIZE: usize = 2048;
const BLOWFISH_BLOCK_SIZE: usize = 8;
const DEEZER_BLOWFISH_IV: [u8; 8] = [0, 1, 2, 3, 4, 5, 6, 7];

pub fn derive_blowfish_key(track_id: &str) -> [u8; 16] {
    let id_md5 = md5_hex(track_id.as_bytes());
    let id_md5_bytes = id_md5.as_bytes();
    let secret_bytes = DEEZER_SECRET.as_bytes();
    let mut key = [0u8; 16];

    for i in 0..16 {
        key[i] = id_md5_bytes[i] ^ id_md5_bytes[i + 16] ^ secret_bytes[i];
    }

    key
}

pub fn decrypt_audio_stream(track_id: &str, encrypted_bytes: &[u8]) -> Result<Vec<u8>> {
    let key = derive_blowfish_key(track_id);
    decrypt_audio_stream_with_key(&key, encrypted_bytes)
}

pub fn decrypt_chunked_stream(track_id: &str, encrypted_bytes: &[u8]) -> Result<Vec<u8>> {
    decrypt_audio_stream(track_id, encrypted_bytes)
}

pub fn decrypt_chunk_in_place_with_key(
    key: &[u8],
    chunk_index: usize,
    chunk: &mut [u8],
) -> Result<()> {
    if key.len() < 4 || key.len() > 56 {
        return Err(anyhow!("invalid Blowfish key length"));
    }

    if chunk_index % 3 != 0 {
        return Ok(());
    }

    let decryptable_len = chunk.len() - (chunk.len() % BLOWFISH_BLOCK_SIZE);
    if decryptable_len == 0 {
        return Ok(());
    }

    let chunk_prefix = &mut chunk[..decryptable_len];
    let cipher = BlowfishCbcDec::<Blowfish>::new_from_slices(key, &DEEZER_BLOWFISH_IV)
        .map_err(|_| anyhow!("failed to initialize Blowfish-CBC decryptor"))?;
    cipher
        .decrypt_padded_mut::<NoPadding>(chunk_prefix)
        .map_err(|_| anyhow!("failed to decrypt chunk using Blowfish-CBC"))?;

    Ok(())
}

pub fn decrypt_audio_stream_with_key(key: &[u8], encrypted_bytes: &[u8]) -> Result<Vec<u8>> {
    let mut output = Vec::with_capacity(encrypted_bytes.len());

    for (chunk_index, chunk) in encrypted_bytes.chunks(CHUNK_SIZE).enumerate() {
        let mut decrypted_chunk = chunk.to_vec();
        decrypt_chunk_in_place_with_key(key, chunk_index, &mut decrypted_chunk)?;
        output.extend_from_slice(&decrypted_chunk);
    }

    Ok(output)
}

fn md5_hex(bytes: &[u8]) -> String {
    let digest = md5::compute(bytes);
    let mut output = String::with_capacity(32);

    for byte in digest.0 {
        let _ = write!(&mut output, "{byte:02x}");
    }

    output
}

#[cfg(test)]
mod tests {
    use super::{
        decrypt_audio_stream_with_key, derive_blowfish_key, Blowfish, BlowfishCbcEnc, NoPadding,
        CHUNK_SIZE, DEEZER_BLOWFISH_IV,
    };
    use blowfish::cipher::{BlockEncryptMut, KeyIvInit};

    #[test]
    fn derived_key_is_stable() {
        let key = derive_blowfish_key("3135556");
        assert_eq!(hex::encode(key), "6c6c666b39662c37652575603c643439");
    }

    #[test]
    fn decrypts_only_every_third_chunk() {
        let key = derive_blowfish_key("42");
        let plaintext = vec![0x5a; CHUNK_SIZE * 4];
        let mut encrypted = plaintext.clone();

        for chunk_index in [0usize, 3usize] {
            let start = chunk_index * CHUNK_SIZE;
            let end = start + CHUNK_SIZE;
            let chunk = &mut encrypted[start..end];
            let cipher = BlowfishCbcEnc::<Blowfish>::new_from_slices(&key, &DEEZER_BLOWFISH_IV)
                .expect("valid key and IV");
            let msg_len = chunk.len();
            cipher
                .encrypt_padded_mut::<NoPadding>(chunk, msg_len)
                .expect("chunk is block aligned");
        }

        let decrypted = decrypt_audio_stream_with_key(&key, &encrypted).expect("decrypts");
        assert_eq!(decrypted, plaintext);
    }
}
