#![allow(dead_code)]

use anyhow::{anyhow, Context, Result};
use reqwest::header::{HeaderMap, HeaderValue, COOKIE, USER_AGENT};
use serde_json::{json, Value};
use std::collections::HashSet;
use crate::config::AudioQuality;

const GATEWAY_URL: &str = "https://www.deezer.com/ajax/gw-light.php";
const MEDIA_URL_API: &str = "https://media.deezer.com/v1/get_url";

#[derive(Clone, Debug)]
pub struct DeezerClient {
    http: reqwest::Client,
    arl: String,
    session_id: String,
    license_token: String,
    api_token: Option<String>,
    user_id: Option<String>,
    loved_tracks_id: Option<String>,
}

#[derive(Clone, Debug)]
pub struct TrackMetadata {
    pub id: String,
    pub title: String,
    pub artist: String,
    pub track_token: String,
    pub duration_secs: Option<u64>,
}

#[derive(Clone, Debug)]
pub struct PlaylistMetadata {
    pub id: String,
    pub title: String,
    pub tracks: Vec<TrackMetadata>,
}

impl DeezerClient {
    pub fn new(arl: impl Into<String>) -> Result<Self> {
        let arl = arl.into();

        let mut headers = HeaderMap::new();
        headers.insert(
            USER_AGENT,
            HeaderValue::from_static(
                "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
            ),
        );
        headers.insert(
            COOKIE,
            HeaderValue::from_str(&format!("arl={arl}"))
                .context("failed to build Deezer cookie header")?,
        );

        let http = reqwest::Client::builder()
            .default_headers(headers)
            .cookie_store(true)
            .build()
            .context("failed to build reqwest client")?;

        Ok(Self {
            http,
            arl,
            session_id: String::new(),
            license_token: String::new(),
            api_token: None,
            user_id: None,
            loved_tracks_id: None,
        })
    }

    pub fn arl(&self) -> &str {
        &self.arl
    }

    pub fn user_id(&self) -> Option<&str> {
        self.user_id.as_deref()
    }

    pub fn loved_tracks_id(&self) -> Option<&str> {
        self.loved_tracks_id.as_deref()
    }

    pub async fn fetch_api_token(&mut self) -> Result<String> {
        let url = format!(
            "{GATEWAY_URL}?method=deezer.getUserData&api_version=1.0&api_token="
        );
        let response_text = self
            .http
            .post(&url)
            .header(reqwest::header::COOKIE, format!("arl={}", self.arl))
            .header(reqwest::header::CONTENT_TYPE, "application/json")
            .body("{}")
            .send()
            .await
            .context("gateway request failed for method deezer.getUserData")?
            .error_for_status()
            .context("gateway returned an error for method deezer.getUserData")?
            .text()
            .await
            .context("failed to read Deezer getUserData response body")?;

        let response = serde_json::from_str::<Value>(&response_text)
            .context("failed to decode Deezer getUserData response")?;

        self.session_id = response["results"]["SESSION_ID"]
            .as_str()
            .map(ToOwned::to_owned)
            .or_else(|| response["results"]["SESSION_ID"].as_i64().map(|id| id.to_string()))
            .unwrap_or_default();

        self.license_token = response["results"]["USER"]["OPTIONS"]["license_token"]
            .as_str()
            .unwrap_or_default()
            .to_owned();

        self.user_id = response["results"]["USER"]["USER_ID"]
            .as_i64()
            .map(|id| id.to_string())
            .or_else(|| response["results"]["USER"]["USER_ID"].as_str().map(ToOwned::to_owned));

        self.loved_tracks_id = response["results"]["USER"]["LOVEDTRACKS_ID"]
            .as_i64()
            .map(|id| id.to_string())
            .or_else(|| {
                response["results"]["USER"]["LOVEDTRACKS_ID"]
                    .as_str()
                    .map(ToOwned::to_owned)
            });

        let token = response["results"]["checkForm"]
            .as_str()
            .ok_or_else(|| anyhow!("gateway response did not contain checkForm api token"))?
            .to_owned();

        self.api_token = Some(token.clone());
        Ok(token)
    }

    pub async fn fetch_track_metadata(&self, track_id: &str) -> Result<TrackMetadata> {
        let api_token = self
            .api_token
            .as_deref()
            .ok_or_else(|| anyhow!("api token not loaded; call fetch_api_token first"))?;

        let url = format!(
            "{GATEWAY_URL}?method=deezer.pageTrack&api_version=1.0&api_token={api_token}"
        );
        let payload = json!({ "sng_id": track_id }).to_string();

        let text = self
            .http
            .post(&url)
            .header(reqwest::header::COOKIE, format!("arl={}; sid={}", self.arl, self.session_id))
            .header(reqwest::header::CONTENT_TYPE, "application/json")
            .body(payload)
            .send()
            .await
            .context("gateway request failed for method deezer.pageTrack")?
            .error_for_status()
            .context("gateway returned an error for method deezer.pageTrack")?
            .text()
            .await?;

        let response = serde_json::from_str::<Value>(&text)
            .context("failed to decode Deezer pageTrack response")?;

        let result = response["results"].get("DATA").unwrap_or(&response["results"]);
        let track_token = match result["TRACK_TOKEN"].as_str() {
            Some(value) => value.to_owned(),
            None => return Err(anyhow!("track metadata missing TRACK_TOKEN")),
        };

        let title = result["SNG_TITLE"]
            .as_str()
            .or_else(|| response["results"]["SNG_TITLE"].as_str())
            .unwrap_or("Unknown track")
            .to_owned();

        let artist = result["ART_NAME"]
            .as_str()
            .or_else(|| response["results"]["ART_NAME"].as_str())
            .unwrap_or("Unknown artist")
            .to_owned();

        let duration_secs = result["DURATION"]
            .as_str()
            .and_then(|s| s.parse::<u64>().ok())
            .or_else(|| result["DURATION"].as_u64());

        Ok(TrackMetadata {
            id: result["SNG_ID"]
                .as_str()
                .unwrap_or(track_id)
                .to_owned(),
            title,
            artist,
            track_token,
            duration_secs,
        })
    }

    pub async fn fetch_media_url(&self, track_token: &str, quality: AudioQuality) -> Result<String> {
        if self.license_token.is_empty() {
            return Err(anyhow!("license token not loaded; call fetch_api_token first"));
        }

        let format = match quality {
            AudioQuality::Kbps128 => "MP3_128",
            AudioQuality::Kbps320 => "MP3_320",
            AudioQuality::Flac => "FLAC",
        };

        let payload = json!({
            "license_token": self.license_token.clone(),
            "media": [{
                "type": "FULL",
                "formats": [{ "cipher": "BF_CBC_STRIPE", "format": format }]
            }],
            "track_tokens": [track_token]
        });

        let response = self
            .http
            .post(MEDIA_URL_API)
            .header(reqwest::header::COOKIE, format!("arl={}; sid={}", self.arl, self.session_id))
            .header(reqwest::header::CONTENT_TYPE, "application/json")
            .json(&payload)
            .send()
            .await
            .context("media.get_url request failed")?
            .error_for_status()
            .context("media.get_url returned error status")?
            .json::<Value>()
            .await
            .context("failed to decode media.get_url response")?;

        response["data"][0]["media"][0]["sources"][0]["url"]
            .as_str()
            .map(ToOwned::to_owned)
            .ok_or_else(|| anyhow!("media.get_url response missing signed source URL"))
    }

    pub async fn fetch_encrypted_bytes_from_signed_url(&self, url: &str) -> Result<Vec<u8>> {
        self.http
            .get(url)
            .send()
            .await
            .context("failed to download encrypted Deezer audio stream")?
            .error_for_status()
            .context("signed CDN request returned an error status")?
            .bytes()
            .await
            .context("failed to read encrypted Deezer audio bytes")
            .map(|bytes| bytes.to_vec())
    }

    pub async fn open_signed_stream(&self, url: &str) -> Result<reqwest::Response> {
        self.http
            .get(url)
            .send()
            .await
            .context("failed to open signed Deezer audio stream")?
            .error_for_status()
            .context("signed Deezer audio stream returned an error status")
    }

    pub async fn fetch_playlist_metadata(&self, playlist_id: &str) -> Result<PlaylistMetadata> {
        let response = self
            .authenticated_gateway_call(
                "deezer.pagePlaylist",
                Some(json!({ "playlist_id": playlist_id, "lang": "en" })),
            )
            .await?;

        let result = &response["results"];
        let title = result["DATA"]["TITLE"]
            .as_str()
            .unwrap_or("Unknown playlist")
            .to_owned();

        let tracks = result["SONGS"]["data"]
            .as_array()
            .into_iter()
            .flatten()
            .filter_map(|track| {
                Some(TrackMetadata {
                    id: track["SNG_ID"].as_str()?.to_owned(),
                    title: track["SNG_TITLE"]
                        .as_str()
                        .unwrap_or("Unknown track")
                        .to_owned(),
                    artist: track["ART_NAME"]
                        .as_str()
                        .unwrap_or("Unknown artist")
                        .to_owned(),
                    track_token: track["TRACK_TOKEN"].as_str()?.to_owned(),
                    duration_secs: track["DURATION"]
                        .as_str()
                        .and_then(|s| s.parse().ok())
                        .or_else(|| track["DURATION"].as_u64()),
                })
            })
            .collect();

        Ok(PlaylistMetadata {
            id: playlist_id.to_owned(),
            title,
            tracks,
        })
    }

    async fn authenticated_gateway_call(&self, method: &str, payload: Option<Value>) -> Result<Value> {
        let api_token = self
            .api_token
            .as_deref()
            .ok_or_else(|| anyhow!("api token not loaded; call fetch_api_token first"))?;

        self.gateway_call(method, payload, Some(api_token)).await
    }

    async fn authenticated_gateway_call_with_raw(
        &self,
        method: &str,
        payload: Option<Value>,
    ) -> Result<(Value, String)> {
        let api_token = self
            .api_token
            .as_deref()
            .ok_or_else(|| anyhow!("api token not loaded; call fetch_api_token first"))?;

        self.gateway_call_with_raw(method, payload, Some(api_token)).await
    }

    async fn gateway_call(
        &self,
        method: &str,
        payload: Option<Value>,
        api_token: Option<&str>,
    ) -> Result<Value> {
        let (response, _) = self
            .gateway_call_with_raw(method, payload, api_token)
            .await?;

        Ok(response)
    }

    async fn gateway_call_with_raw(
        &self,
        method: &str,
        payload: Option<Value>,
        api_token: Option<&str>,
    ) -> Result<(Value, String)> {
        let text = self
            .http
            .post(GATEWAY_URL)
            .query(&[
                ("method", method),
                ("api_version", "1.0"),
                ("input", "3"),
                ("api_token", api_token.unwrap_or("null")),
            ])
            .json(&payload.unwrap_or_else(|| json!({})))
            .send()
            .await
            .with_context(|| format!("gateway request failed for method {method}"))?
            .error_for_status()
            .with_context(|| format!("gateway returned an error for method {method}"))?
            .text()
            .await
            .context("failed to read Deezer gateway response body")?;

        let response = serde_json::from_str::<Value>(&text)
            .context("failed to decode Deezer gateway response")?;

        Ok((response, text))
    }

    pub async fn fetch_user_playlists(&self, user_id: &str) -> Result<Vec<(String, String)>> {
        let api_token = self
            .api_token
            .as_deref()
            .ok_or_else(|| anyhow!("api token not loaded; call fetch_api_token first"))?;
        let effective_user_id = self.user_id.as_deref().unwrap_or(user_id);
        let profile_id = effective_user_id.parse::<u64>().unwrap_or(0);
        let payload = json!({
            "profile_id": profile_id,
            "user_id": profile_id,
            "USER_ID": profile_id,
            "tab": "playlists",
            "nb": 40
        });

        let json = self
            .http
            .post(format!(
                "{GATEWAY_URL}?method=deezer.pageProfile&api_version=1.0&api_token={api_token}"
            ))
            .query(&[("input", "3")])
            .header(
                reqwest::header::COOKIE,
                format!("arl={}; sid={}", self.arl, self.session_id),
            )
            .header(reqwest::header::CONTENT_TYPE, "application/json")
            .json(&payload)
            .send()
            .await?;
        let json = json
            .error_for_status()
            .context("gateway returned error for deezer.pageProfile")?
            .json::<Value>()
            .await
            .context("failed to decode playlist response")?;

        let playlists = json["results"]["TAB"]["playlists"]["data"]
            .as_array()
            .into_iter()
            .flatten()
            .filter_map(|item| {
                let id = item["PLAYLIST_ID"]
                    .as_str()
                    .map(|s| s.to_string())
                    .or_else(|| item["PLAYLIST_ID"].as_i64().map(|n| n.to_string()))
                    .or_else(|| item["id"].as_str().map(|s| s.to_string()))
                    .or_else(|| item["id"].as_i64().map(|n| n.to_string()))
                    .unwrap_or_else(|| {
                        item["id"]
                            .as_i64()
                            .or_else(|| item["PLAYLIST_ID"].as_i64())
                            .unwrap_or(0)
                            .to_string()
                    });

                let title = item["TITLE"]
                    .as_str()
                    .or(item["title"].as_str())?
                    .to_owned();
                Some((id, title))
            })
            .collect();

        Ok(playlists)
    }

    pub async fn fetch_playlist_tracks(&self, playlist_id: &str) -> Result<Vec<(String, String, String)>> {
        let api_token = self
            .api_token
            .as_deref()
            .ok_or_else(|| anyhow!("api token not loaded; call fetch_api_token first"))?;

        let mut all_tracks: Vec<(String, String, String)> = Vec::new();
        let mut seen_ids: HashSet<String> = HashSet::new();
        let mut start = 0usize;
        let page_size = 200usize;
        let mut total_hint: Option<usize> = None;

        loop {
            let payload = json!({
                "playlist_id": playlist_id,
                "lang": "en",
                "header": true,
                "start": start,
                "nb": page_size
            });

            let raw = self
                .http
                .post(format!(
                    "{GATEWAY_URL}?method=deezer.pagePlaylist&api_version=1.0&api_token={api_token}"
                ))
                .query(&[("input", "3")])
                .header(
                    reqwest::header::COOKIE,
                    format!("arl={}; sid={}", self.arl, self.session_id),
                )
                .header(reqwest::header::CONTENT_TYPE, "application/json")
                .json(&payload)
                .send()
                .await
                .context("gateway request failed for deezer.pagePlaylist")?
                .error_for_status()
                .context("gateway returned error for deezer.pagePlaylist")?
                .text()
                .await
                .context("failed to read playlist tracks response body")?;

            let response = serde_json::from_str::<Value>(&raw)
                .context("failed to decode playlist tracks response")?;

            if !response["error"].is_null()
                && response["error"] != json!([])
                && response["error"] != json!({})
            {
                return Err(anyhow!("playlist gateway error: {}", response["error"]));
            }

            let track_list = response["results"]["SONGS"]["data"]
                .as_array()
                .or_else(|| response["results"]["DATA"]["SONGS"]["data"].as_array())
                .or_else(|| response["results"]["tracks"]["data"].as_array())
                .or_else(|| response["results"]["TRACKS"]["data"].as_array())
                .or_else(|| response["results"]["tracks"].as_array())
                .or_else(|| response["results"]["SONGS"].as_array());

            let current_page_tracks: Vec<(String, String, String)> = track_list
                .into_iter()
                .flatten()
                .filter_map(|track| {
                    let id = track["SNG_ID"]
                        .as_str()
                        .map(|s| s.to_owned())
                        .or_else(|| track["SNG_ID"].as_i64().map(|n| n.to_string()))
                        .or_else(|| track["id"].as_str().map(|s| s.to_owned()))
                        .or_else(|| track["id"].as_i64().map(|n| n.to_string()))?;

                    let title = track["SNG_TITLE"]
                        .as_str()
                        .or(track["title"].as_str())
                        .unwrap_or("Unknown track")
                        .to_owned();

                    let artist = track["ART_NAME"]
                        .as_str()
                        .or(track["artist"]["name"].as_str())
                        .or(track["ARTISTS"][0]["ART_NAME"].as_str())
                        .unwrap_or("Unknown artist")
                        .to_owned();

                    Some((id, title, artist))
                })
                .collect();

            if total_hint.is_none() {
                total_hint = response["results"]["SONGS"]["total"]
                    .as_u64()
                    .map(|n| n as usize)
                    .or_else(|| response["results"]["SONGS"]["count"].as_u64().map(|n| n as usize))
                    .or_else(|| response["results"]["tracks"]["total"].as_u64().map(|n| n as usize));
            }

            for (id, title, artist) in current_page_tracks.iter() {
                if seen_ids.insert(id.clone()) {
                    all_tracks.push((id.clone(), title.clone(), artist.clone()));
                }
            }

            if current_page_tracks.is_empty() || current_page_tracks.len() < page_size {
                break;
            }

            if let Some(total) = total_hint {
                if all_tracks.len() >= total {
                    break;
                }
            }

            start += page_size;
        }

        Ok(all_tracks)
    }

    pub async fn fetch_favorite_tracks(&self) -> Result<Vec<(String, String, String)>> {
        let loved_id = self
            .loved_tracks_id
            .as_deref()
            .ok_or_else(|| anyhow!("LOVEDTRACKS_ID missing; call fetch_api_token first"))?;
        self.fetch_playlist_tracks(loved_id).await
    }

    pub async fn fetch_search_results(
        &self,
        query: &str,
    ) -> Result<(
        Vec<(String, String, String)>,
        Vec<(String, String)>,
        Vec<(String, String)>,
    )> {
        let api_token = self
            .api_token
            .as_deref()
            .ok_or_else(|| anyhow!("api token not loaded; call fetch_api_token first"))?;

        let payload = json!({
            "query": query,
            "QUERY": query,
            "start": 0,
            "nb": 50,
            "suggest": true,
            "artist_suggest": true,
            "top_tracks": true
        });

        let raw = self
            .http
            .post(format!(
                "{GATEWAY_URL}?method=deezer.pageSearch&api_version=1.0&api_token={api_token}"
            ))
            .query(&[("input", "3")])
            .header(
                reqwest::header::COOKIE,
                format!("arl={}; sid={}", self.arl, self.session_id),
            )
            .header(reqwest::header::CONTENT_TYPE, "application/json")
            .json(&payload)
            .send()
            .await
            .context("gateway request failed for deezer.pageSearch")?
            .error_for_status()
            .context("gateway returned error for deezer.pageSearch")?
            .text()
            .await
            .context("failed to read search response body")?;

        let response = serde_json::from_str::<Value>(&raw)
            .context("failed to decode search response")?;

        if !response["error"].is_null()
            && response["error"] != json!([])
            && response["error"] != json!({})
        {
            return Err(anyhow!("search gateway error: {}", response["error"]));
        }

        let tracks = response["results"]["TRACK"]["data"]
            .as_array()
            .or_else(|| response["results"]["SONGS"]["data"].as_array())
            .or_else(|| response["results"]["tracks"]["data"].as_array())
            .into_iter()
            .flatten()
            .filter_map(|track| {
                let id = track["SNG_ID"]
                    .as_str()
                    .map(|s| s.to_owned())
                    .or_else(|| track["SNG_ID"].as_i64().map(|n| n.to_string()))?;
                let title = track["SNG_TITLE"].as_str()?.to_owned();
                let artist = track["ART_NAME"].as_str()?.to_owned();
                Some((id, title, artist))
            })
            .collect();

        let playlists = response["results"]["PLAYLIST"]["data"]
            .as_array()
            .or_else(|| response["results"]["PLAYLISTS"]["data"].as_array())
            .into_iter()
            .flatten()
            .filter_map(|playlist| {
                let id = playlist["PLAYLIST_ID"]
                    .as_str()
                    .map(|s| s.to_owned())
                    .or_else(|| playlist["PLAYLIST_ID"].as_i64().map(|n| n.to_string()))?;
                let title = playlist["TITLE"]
                    .as_str()
                    .or(playlist["title"].as_str())?
                    .to_owned();
                Some((id, title))
            })
            .collect();

        let artists = response["results"]["ARTIST"]["data"]
            .as_array()
            .or_else(|| response["results"]["ARTISTS"]["data"].as_array())
            .into_iter()
            .flatten()
            .filter_map(|artist| {
                let id = artist["ART_ID"]
                    .as_str()
                    .map(|s| s.to_owned())
                    .or_else(|| artist["ART_ID"].as_i64().map(|n| n.to_string()))?;
                let name = artist["ART_NAME"]
                    .as_str()
                    .or(artist["name"].as_str())?
                    .to_owned();
                Some((id, name))
            })
            .collect();

        Ok((tracks, playlists, artists))
    }

}
