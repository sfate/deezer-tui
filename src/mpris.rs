use std::sync::RwLock;

use mpris_server::{
    zbus::{self, fdo},
    LoopStatus, Metadata, PlaybackRate, PlaybackStatus, PlayerInterface, Property, RootInterface,
    Server, Signal, Time, TrackId, Volume,
};

// ---------------------------------------------------------------------------
// Events sent FROM the MPRIS D-Bus interface TO the TUI event loop
// ---------------------------------------------------------------------------
#[derive(Debug, Clone)]
pub enum MprisEvent {
    Play,
    Pause,
    Toggle,
    Next,
    Previous,
    /// Relative seek by `offset_us` microseconds (can be negative).
    SeekBy(i64),
    /// Absolute seek to `pos_us` microseconds from track start.
    SetPosition(i64),
    SetShuffle(bool),
    SetLoopStatus(LoopStatus),
    SetVolume(f64),
}

// ---------------------------------------------------------------------------
// Shared state (read by D-Bus property getters, written by TUI updates)
// ---------------------------------------------------------------------------
pub struct MprisState {
    pub playback_status: PlaybackStatus,
    pub loop_status: LoopStatus,
    pub shuffle: bool,
    pub metadata: Metadata,
    pub position_us: i64,
    pub volume: f64,
    pub can_go_next: bool,
    pub can_go_previous: bool,
}

// ---------------------------------------------------------------------------
// The MPRIS player implementation used by mpris_server::Server<T>
// ---------------------------------------------------------------------------
pub struct MprisPlayer {
    pub state: RwLock<MprisState>,
    event_tx: crossbeam_channel::Sender<MprisEvent>,
}

impl MprisPlayer {
    pub fn new(event_tx: crossbeam_channel::Sender<MprisEvent>) -> Self {
        Self {
            state: RwLock::new(MprisState {
                playback_status: PlaybackStatus::Stopped,
                loop_status: LoopStatus::None,
                shuffle: false,
                metadata: Metadata::new(),
                position_us: 0,
                volume: 1.0,
                can_go_next: false,
                can_go_previous: false,
            }),
            event_tx,
        }
    }
}

impl RootInterface for MprisPlayer {
    async fn can_quit(&self) -> fdo::Result<bool> { Ok(false) }
    async fn quit(&self) -> fdo::Result<()> { Ok(()) }
    async fn can_raise(&self) -> fdo::Result<bool> { Ok(false) }
    async fn raise(&self) -> fdo::Result<()> { Ok(()) }
    async fn has_track_list(&self) -> fdo::Result<bool> { Ok(false) }
    async fn identity(&self) -> fdo::Result<String> { Ok("Deezer TUI".to_owned()) }
    async fn desktop_entry(&self) -> fdo::Result<String> { Ok(String::new()) }
    async fn supported_uri_schemes(&self) -> fdo::Result<Vec<String>> { Ok(vec![]) }
    async fn supported_mime_types(&self) -> fdo::Result<Vec<String>> { Ok(vec![]) }
    async fn fullscreen(&self) -> fdo::Result<bool> { Ok(false) }
    async fn set_fullscreen(&self, _fullscreen: bool) -> zbus::Result<()> { Ok(()) }
    async fn can_set_fullscreen(&self) -> fdo::Result<bool> { Ok(false) }
}

impl PlayerInterface for MprisPlayer {
    async fn next(&self) -> fdo::Result<()> {
        let _ = self.event_tx.send(MprisEvent::Next);
        Ok(())
    }
    async fn previous(&self) -> fdo::Result<()> {
        let _ = self.event_tx.send(MprisEvent::Previous);
        Ok(())
    }
    async fn pause(&self) -> fdo::Result<()> {
        let _ = self.event_tx.send(MprisEvent::Pause);
        Ok(())
    }
    async fn play_pause(&self) -> fdo::Result<()> {
        let _ = self.event_tx.send(MprisEvent::Toggle);
        Ok(())
    }
    async fn stop(&self) -> fdo::Result<()> {
        let _ = self.event_tx.send(MprisEvent::Pause);
        Ok(())
    }
    async fn play(&self) -> fdo::Result<()> {
        let _ = self.event_tx.send(MprisEvent::Play);
        Ok(())
    }
    async fn seek(&self, offset: Time) -> fdo::Result<()> {
        let _ = self.event_tx.send(MprisEvent::SeekBy(offset.as_micros()));
        Ok(())
    }
    async fn set_position(&self, _track_id: TrackId, position: Time) -> fdo::Result<()> {
        let _ = self.event_tx.send(MprisEvent::SetPosition(position.as_micros()));
        Ok(())
    }
    async fn open_uri(&self, _uri: String) -> fdo::Result<()> { Ok(()) }

    async fn playback_status(&self) -> fdo::Result<PlaybackStatus> {
        Ok(self.state.read().unwrap().playback_status)
    }
    async fn loop_status(&self) -> fdo::Result<LoopStatus> {
        Ok(self.state.read().unwrap().loop_status)
    }
    async fn set_loop_status(&self, loop_status: LoopStatus) -> zbus::Result<()> {
        self.state.write().unwrap().loop_status = loop_status;
        let _ = self.event_tx.send(MprisEvent::SetLoopStatus(loop_status));
        Ok(())
    }
    async fn rate(&self) -> fdo::Result<PlaybackRate> { Ok(PlaybackRate::from(1.0)) }
    async fn set_rate(&self, _rate: PlaybackRate) -> zbus::Result<()> { Ok(()) }
    async fn shuffle(&self) -> fdo::Result<bool> {
        Ok(self.state.read().unwrap().shuffle)
    }
    async fn set_shuffle(&self, shuffle: bool) -> zbus::Result<()> {
        self.state.write().unwrap().shuffle = shuffle;
        let _ = self.event_tx.send(MprisEvent::SetShuffle(shuffle));
        Ok(())
    }
    async fn metadata(&self) -> fdo::Result<Metadata> {
        Ok(self.state.read().unwrap().metadata.clone())
    }
    async fn volume(&self) -> fdo::Result<Volume> {
        Ok(Volume::from(self.state.read().unwrap().volume))
    }
    async fn set_volume(&self, volume: Volume) -> zbus::Result<()> {
        let v = f64::from(volume).clamp(0.0, 1.0);
        self.state.write().unwrap().volume = v;
        let _ = self.event_tx.send(MprisEvent::SetVolume(v));
        Ok(())
    }
    async fn position(&self) -> fdo::Result<Time> {
        Ok(Time::from_micros(self.state.read().unwrap().position_us))
    }
    async fn minimum_rate(&self) -> fdo::Result<PlaybackRate> { Ok(PlaybackRate::from(1.0)) }
    async fn maximum_rate(&self) -> fdo::Result<PlaybackRate> { Ok(PlaybackRate::from(1.0)) }
    async fn can_go_next(&self) -> fdo::Result<bool> {
        Ok(self.state.read().unwrap().can_go_next)
    }
    async fn can_go_previous(&self) -> fdo::Result<bool> {
        Ok(self.state.read().unwrap().can_go_previous)
    }
    async fn can_play(&self) -> fdo::Result<bool> { Ok(true) }
    async fn can_pause(&self) -> fdo::Result<bool> { Ok(true) }
    async fn can_seek(&self) -> fdo::Result<bool> { Ok(true) }
    async fn can_control(&self) -> fdo::Result<bool> { Ok(true) }
}

// ---------------------------------------------------------------------------
// Helper to build a Metadata dict from track info
// ---------------------------------------------------------------------------
pub fn build_metadata(
    track_id: &str,
    title: &str,
    artist: &str,
    art_url: Option<&str>,
    duration_ms: u64,
) -> Metadata {
    let mut m = Metadata::new();
    let path = format!("/com/deezer_tui/track/{}", track_id.replace('-', "_"));
    if let Ok(tid) = TrackId::try_from(path.as_str()) {
        m.set_trackid(Some(tid));
    }
    m.set_title(Some(title.to_owned()));
    m.set_artist(Some(vec![artist.to_owned()]));
    if let Some(url) = art_url {
        m.set_art_url(Some(url.to_owned()));
    }
    if duration_ms > 0 {
        m.set_length(Some(Time::from_micros(duration_ms as i64 * 1_000)));
    }
    m
}

// ---------------------------------------------------------------------------
// Public factory: creates the Server and returns the crossbeam event receiver
// ---------------------------------------------------------------------------
pub async fn create_server() -> Option<(Server<MprisPlayer>, crossbeam_channel::Receiver<MprisEvent>)> {
    let (tx, rx) = crossbeam_channel::unbounded::<MprisEvent>();
    let player = MprisPlayer::new(tx);
    let server = Server::new("deezer-tui", player).await.ok()?;
    Some((server, rx))
}

// ---------------------------------------------------------------------------
// Convenience: update playback state properties on the server
// ---------------------------------------------------------------------------
pub async fn set_playback_status(
    server: &Server<MprisPlayer>,
    status: PlaybackStatus,
    position_ms: u64,
) {
    let pos_us = position_ms as i64 * 1_000;
    server.imp().state.write().unwrap().playback_status = status;
    server.imp().state.write().unwrap().position_us = pos_us;
    let _ = server
        .properties_changed([Property::PlaybackStatus(status)])
        .await;
}

pub async fn set_track_metadata(
    server: &Server<MprisPlayer>,
    metadata: Metadata,
    position_ms: u64,
    can_go_next: bool,
    can_go_previous: bool,
) {
    let pos_us = position_ms as i64 * 1_000;
    {
        let mut state = server.imp().state.write().unwrap();
        state.metadata = metadata.clone();
        state.playback_status = PlaybackStatus::Playing;
        state.position_us = pos_us;
        state.can_go_next = can_go_next;
        state.can_go_previous = can_go_previous;
    }
    let _ = server
        .properties_changed([
            Property::Metadata(metadata),
            Property::PlaybackStatus(PlaybackStatus::Playing),
            Property::CanGoNext(can_go_next),
            Property::CanGoPrevious(can_go_previous),
        ])
        .await;
}

pub async fn notify_seeked(server: &Server<MprisPlayer>, position_ms: u64) {
    let pos_us = position_ms as i64 * 1_000;
    server.imp().state.write().unwrap().position_us = pos_us;
    let _ = server
        .emit(Signal::Seeked {
            position: Time::from_micros(pos_us),
        })
        .await;
}

pub async fn update_loop_and_shuffle(
    server: &Server<MprisPlayer>,
    loop_status: LoopStatus,
    shuffle: bool,
) {
    {
        let mut state = server.imp().state.write().unwrap();
        state.loop_status = loop_status;
        state.shuffle = shuffle;
    }
    let _ = server
        .properties_changed([
            Property::LoopStatus(loop_status),
            Property::Shuffle(shuffle),
        ])
        .await;
}

pub fn update_position(server: &Server<MprisPlayer>, position_ms: u64) {
    server.imp().state.write().unwrap().position_us = position_ms as i64 * 1_000;
}
