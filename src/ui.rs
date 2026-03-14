use ratatui::{
    layout::{Alignment, Constraint, Direction, Layout},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Gauge, List, ListItem, Padding, Paragraph},
};

use crate::{
    app::{ActivePanel, App, RepeatMode, SearchCategory},
    config::AudioQuality,
};

fn quality_label(quality: AudioQuality) -> &'static str {
    match quality {
        AudioQuality::Kbps128 => "128kbps",
        AudioQuality::Kbps320 => "320kbps",
        AudioQuality::Flac => "FLAC",
    }
}

fn get_border_style(app: &App, panel: ActivePanel) -> Style {
    if app.active_panel == panel {
        Style::default().fg(Color::Magenta)
    } else {
        Style::default().fg(Color::DarkGray)
    }
}

fn panel_is_active(app: &App, panel: ActivePanel) -> bool {
    app.active_panel == panel
}

pub fn render(f: &mut ratatui::Frame<'_>, app: &mut App) {
    let accent = Color::Magenta;

    // Root layout: main content + player bar
    let root = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Min(0), Constraint::Length(6)])
        .split(f.size());

    // Workspace: sidebar + main content
    let workspace = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Length(32), Constraint::Min(0)])
        .split(root[0]);

    // Sidebar layout
    let sidebar = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(5),
            Constraint::Percentage(50),
            Constraint::Percentage(50),
            Constraint::Length(1),
        ])
        .split(workspace[0]);

    let highlight_style = Style::default()
        .fg(accent)
        .add_modifier(Modifier::BOLD);

    // Navigation menu
    let nav_items = vec![
        ListItem::new("Home"),
        ListItem::new("Explore"),
        ListItem::new("Favorites"),
        ListItem::new("Settings"),
    ];

    let nav_list = List::new(nav_items)
        .style(Style::default().fg(Color::White))
        .highlight_style(highlight_style)
        .highlight_symbol(" > ")
        .block(
            Block::default()
                .title("Menu")
                .borders(Borders::ALL)
                .border_style(get_border_style(app, ActivePanel::Navigation))
                .padding(Padding::new(1, 0, 0, 0)),
        );
    f.render_stateful_widget(nav_list, sidebar[0], &mut app.nav_state);

    // Playlists list
    let playlist_items: Vec<ListItem<'_>> = app
        .playlists
        .iter()
        .map(|(_, title)| ListItem::new(title.as_str()))
        .collect();

    let playlist_list = List::new(playlist_items)
        .style(Style::default().fg(Color::White))
        .highlight_style(highlight_style)
        .highlight_symbol(" > ")
        .block(
            Block::default()
                .title("Playlists")
                .borders(Borders::ALL)
                .border_style(get_border_style(app, ActivePanel::Playlists))
                .padding(Padding::new(1, 0, 0, 0)),
        );
    f.render_stateful_widget(playlist_list, sidebar[1], &mut app.playlist_state);

    // Queue list
    let queue_items: Vec<ListItem<'_>> = app
        .queue
        .iter()
        .map(|name| ListItem::new(name.as_str()))
        .collect();

    let queue_list = List::new(queue_items)
        .style(Style::default().fg(Color::White))
        .highlight_style(highlight_style)
        .highlight_symbol(" > ")
        .block(
            Block::default()
                .title(format!("Queue ({})", app.queue.len()))
                .borders(Borders::ALL)
                .border_style(get_border_style(app, ActivePanel::Queue))
                .padding(Padding::new(1, 0, 0, 0)),
        );
    f.render_stateful_widget(queue_list, sidebar[2], &mut app.queue_state);

    // Status bar
    let status_bar = Paragraph::new(app.status_message.as_str())
        .style(Style::default().fg(Color::Yellow))
        .alignment(Alignment::Left);
    f.render_widget(status_bar, sidebar[3]);

    // Main content area layout: search bar + main content
    let main_sections = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(3), Constraint::Min(0)])
        .split(workspace[1]);

    // Search bar
    let search_bar_text = if app.is_searching {
        format!("🔍 {}_", app.search_query)
    } else {
        format!("🔍 {}", app.search_query)
    };

    let search_border_style = if app.is_searching || panel_is_active(app, ActivePanel::Search) {
        Style::default().fg(Color::Magenta)
    } else {
        Style::default().fg(Color::DarkGray)
    };

    let search_bar = Paragraph::new(search_bar_text)
        .block(
            Block::default()
                .borders(Borders::ALL)
                .border_style(search_border_style)
                .padding(Padding::new(1, 1, 0, 0)),
        )
        .style(Style::default().fg(Color::White));
    f.render_widget(search_bar, main_sections[0]);

    // Main content sections: header + list
    let main_content_sections = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(6), Constraint::Min(0)])
        .split(main_sections[1]);

    // Main view header
    let header = if app.viewing_settings {
        Paragraph::new(vec![
            Line::from(Span::styled(
                "Settings",
                Style::default().fg(Color::White).add_modifier(Modifier::BOLD),
            )),
            Line::from(Span::styled(
                "Customize your playback experience",
                Style::default().fg(Color::DarkGray),
            )),
        ])
    } else if app.showing_search_results {
        let tracks_tab = if app.search_category == SearchCategory::Tracks {
            "[Tracks]"
        } else {
            " Tracks "
        };
        let playlists_tab = if app.search_category == SearchCategory::Playlists {
            "[Playlists]"
        } else {
            " Playlists "
        };
        let artists_tab = if app.search_category == SearchCategory::Artists {
            "[Artists]"
        } else {
            " Artists "
        };

        Paragraph::new(vec![
            Line::from(Span::styled(
                "Search Results",
                Style::default().fg(Color::White).add_modifier(Modifier::BOLD),
            )),
            Line::from(Span::styled(
                format!("{}  {}  {}", tracks_tab, playlists_tab, artists_tab),
                Style::default().fg(Color::Magenta).add_modifier(Modifier::BOLD),
            )),
            Line::from(Span::styled(
                format!(
                    "Tracks: {}  Playlists: {}  Artists: {}",
                    app.current_tracks.len(),
                    app.search_playlists.len(),
                    app.search_artists.len()
                ),
                Style::default().fg(Color::DarkGray),
            )),
            Line::from(Span::styled(
                format!(
                    "Top playlists: {}",
                    app.search_playlists
                        .iter()
                        .take(2)
                        .map(|(_, t)| t.as_str())
                        .collect::<Vec<_>>()
                        .join(", ")
                ),
                Style::default().fg(Color::DarkGray),
            )),
            Line::from(Span::styled(
                format!(
                    "Top artists: {}",
                    app.search_artists
                        .iter()
                        .take(2)
                        .map(|(_, a)| a.as_str())
                        .collect::<Vec<_>>()
                        .join(", ")
                ),
                Style::default().fg(Color::DarkGray),
            )),
        ])
    } else if !app.current_tracks.is_empty() {
        let playlist_id = app.current_playlist_id.as_deref().unwrap_or("Unknown");
        let playlist_name = app
            .current_playlist_id
            .as_ref()
            .and_then(|id| {
                app.playlists
                    .iter()
                    .find(|(pid, _)| pid == id)
                    .map(|(_, title)| title.as_str())
            })
            .or_else(|| {
                app.search_playlists
                    .iter()
                    .find(|(pid, _)| Some(pid) == app.current_playlist_id.as_ref())
                    .map(|(_, title)| title.as_str())
            })
            .unwrap_or("Playlist");

        Paragraph::new(vec![
            Line::from(Span::styled(
                format!("Playlist: {}({})", playlist_name, playlist_id),
                Style::default().fg(Color::White).add_modifier(Modifier::BOLD),
            )),
            Line::from(Span::styled(
                format!("{} tracks", app.current_tracks.len()),
                Style::default().fg(Color::DarkGray),
            )),
        ])
    } else {
        Paragraph::new(vec![
            Line::from(Span::styled(
                "Flow",
                Style::default().fg(Color::White).add_modifier(Modifier::BOLD),
            )),
            Line::from(Span::styled(
                "Made for you - nonstop music tuned to your taste",
                Style::default().fg(Color::DarkGray),
            )),
        ])
    };

    let main_title = if app.viewing_settings {
        "Settings"
    } else if app.showing_search_results {
        match app.search_category {
            SearchCategory::Tracks => "Search: Tracks",
            SearchCategory::Playlists => "Search: Playlists",
            SearchCategory::Artists => "Search: Artists",
        }
    } else if !app.current_tracks.is_empty() {
        "Tracks"
    } else {
        "Flow / Explore"
    };

    let header_block = header.block(
        Block::default()
            .title(main_title)
            .borders(Borders::ALL)
            .border_style(get_border_style(app, ActivePanel::Main))
            .padding(Padding::new(2, 2, 1, 0)),
    );
    f.render_widget(header_block, main_content_sections[0]);

    // Main content area
    if app.viewing_settings {
        // Settings view
        let settings_items = vec![
            format!(
                "Crossfade: [{}]",
                if app.config.crossfade_enabled { "On" } else { "Off" }
            ),
            format!(
                "Crossfade Duration: {}ms",
                app.config.crossfade_duration_ms
            ),
            format!("Quality: [{}]", quality_label(app.config.default_quality)),
            format!(
                "Discord RPC: [{}]",
                if app.discord_rpc_enabled { "On" } else { "Off" }
            ),
            "Set ARL from search input".to_string(),
        ];

        let settings_list_items: Vec<ListItem> = settings_items
            .iter()
            .map(|item| ListItem::new(item.as_str()))
            .collect();

        let settings_list = List::new(settings_list_items)
            .style(Style::default().fg(Color::White))
            .highlight_style(highlight_style)
            .highlight_symbol(" > ")
            .block(
                Block::default()
                    .borders(Borders::ALL)
                    .border_style(Style::default().fg(Color::DarkGray))
                    .padding(Padding::new(1, 1, 0, 0)),
            );

        f.render_stateful_widget(settings_list, main_content_sections[1], &mut app.settings_state);
    } else if app.showing_search_results {
        // Tracks list view
        let track_items: Vec<ListItem> = match app.search_category {
            SearchCategory::Tracks => {
                let mut items: Vec<ListItem> = vec![ListItem::new("[ Play Playlist ]")];
                items.extend(app.current_tracks.iter().map(|(_, title, artist)| {
                    ListItem::new(format!("{} - {}", title, artist))
                }));
                items
            }
            SearchCategory::Playlists => app
                .search_playlists
                .iter()
                .map(|(_, title)| ListItem::new(format!("{}", title)))
                .collect(),
            SearchCategory::Artists => app
                .search_artists
                .iter()
                .map(|(_, name)| ListItem::new(format!("{}", name)))
                .collect(),
        };

        let empty_hint = track_items.is_empty();

        let tracks_list = List::new(track_items)
            .style(Style::default().fg(Color::White))
            .highlight_style(highlight_style)
            .highlight_symbol(" > ")
            .block(
                Block::default()
                    .borders(Borders::ALL)
                    .border_style(Style::default().fg(Color::DarkGray))
                    .padding(Padding::new(1, 1, 0, 0)),
            );

        if empty_hint {
            f.render_widget(
                Paragraph::new("No results in this category")
                    .style(Style::default().fg(Color::DarkGray))
                    .block(
                        Block::default()
                            .borders(Borders::ALL)
                            .border_style(Style::default().fg(Color::DarkGray))
                            .padding(Padding::new(1, 1, 0, 0)),
                    ),
                main_content_sections[1],
            );
        } else {
            f.render_stateful_widget(tracks_list, main_content_sections[1], &mut app.main_state);
        }
    } else if !app.current_tracks.is_empty() {
        let mut track_items: Vec<ListItem> = vec![ListItem::new("[ Play Playlist ]")];
        track_items.extend(app.current_tracks.iter().map(|(_, title, artist)| {
            ListItem::new(format!("{} - {}", title, artist))
        }));

        let tracks_list = List::new(track_items)
            .style(Style::default().fg(Color::White))
            .highlight_style(highlight_style)
            .highlight_symbol(" > ")
            .block(
                Block::default()
                    .borders(Borders::ALL)
                    .border_style(Style::default().fg(Color::DarkGray))
                    .padding(Padding::new(1, 1, 0, 0)),
            );

        f.render_stateful_widget(tracks_list, main_content_sections[1], &mut app.main_state);
    } else {
        // Default discovery view
        let recently_played = Paragraph::new(vec![
            Line::from(Span::styled(
                "Recently Played",
                Style::default().fg(Color::White).add_modifier(Modifier::BOLD),
            )),
            Line::from(Span::styled("Venom", Style::default().fg(Color::DarkGray))),
            Line::from(Span::styled("Lose Yourself", Style::default().fg(Color::DarkGray))),
            Line::from(Span::styled("Mockingbird", Style::default().fg(Color::DarkGray))),
            Line::from(Span::styled("Godzilla", Style::default().fg(Color::DarkGray))),
        ])
        .block(
            Block::default()
                .title("Discover")
                .borders(Borders::ALL)
                .border_style(Style::default().fg(Color::DarkGray))
                .padding(Padding::new(2, 2, 1, 1)),
        );
        f.render_widget(recently_played, main_content_sections[1]);
    }

    // Player bar layout
    let player_bar = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([
            Constraint::Percentage(25),
            Constraint::Percentage(50),
            Constraint::Percentage(25),
        ])
        .split(root[1]);

    let (track_title, track_artist, current_ms, total_ms, quality) = match &app.now_playing {
        Some(now) => (
            now.title.clone(),
            now.artist.clone(),
            now.current_ms,
            now.total_ms.max(1),
            now.quality,
        ),
        None => (
            "No track".to_owned(),
            "-".to_owned(),
            0,
            1,
            app.config.default_quality,
        ),
    };

    let track_info = Paragraph::new(vec![
        Line::from(Span::styled(
            track_title,
            Style::default().fg(Color::White).add_modifier(Modifier::BOLD),
        )),
        Line::from(Span::styled(track_artist, Style::default().fg(Color::DarkGray))),
    ])
    .block(
        Block::default()
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::DarkGray))
            .padding(Padding::new(1, 1, 0, 0)),
    );
    f.render_widget(track_info, player_bar[0]);

    let controls_and_progress = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(2), Constraint::Length(2)])
        .split(player_bar[1]);

    let play_symbol = if app.is_playing { "Pause" } else { "Play" };
    let labels = [
        "Shuffle".to_string(),
        "Prev".to_string(),
        play_symbol.to_string(),
        "Next".to_string(),
        format!(
            "Repeat:{}",
            match app.repeat_mode {
                RepeatMode::Off => "Off",
                RepeatMode::All => "All",
                RepeatMode::One => "One",
            }
        ),
    ];

    let mut control_spans = Vec::new();
    for (i, label) in labels.iter().enumerate() {
        let active = app.active_panel == ActivePanel::Player && app.player_button_index == i;
        let style = if active {
            Style::default().fg(Color::Black).bg(Color::Magenta).add_modifier(Modifier::BOLD)
        } else {
            Style::default().fg(Color::White)
        };
        control_spans.push(Span::styled(format!("[{}]", label), style));
        control_spans.push(Span::raw("  "));
    }

    let controls = Paragraph::new(Line::from(control_spans))
        .alignment(Alignment::Center)
        .style(if app.active_panel == ActivePanel::Player {
            Style::default().fg(Color::White)
        } else {
            Style::default().fg(Color::DarkGray)
        });
    f.render_widget(controls, controls_and_progress[0]);

    let ratio = (current_ms as f64 / total_ms as f64).clamp(0.0, 1.0);
    let seeking_active = app.active_panel == ActivePanel::PlayerProgress;

    // Split progress row into: current time | gauge | total time
    let progress_row = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Length(5), Constraint::Min(0), Constraint::Length(5)])
        .split(controls_and_progress[1]);

    let cur_min = current_ms / 60_000;
    let cur_sec = (current_ms / 1_000) % 60;
    let tot_min = total_ms / 60_000;
    let tot_sec = (total_ms / 1_000) % 60;

    let time_style = if seeking_active {
        Style::default().fg(Color::Yellow).add_modifier(Modifier::BOLD)
    } else {
        Style::default().fg(Color::DarkGray)
    };

    f.render_widget(
        Paragraph::new(format!("{:02}:{:02}", cur_min, cur_sec))
            .style(time_style)
            .alignment(Alignment::Right),
        progress_row[0],
    );

    let gauge = Gauge::default()
        .style(Style::default().fg(Color::DarkGray))
        .gauge_style(
            if seeking_active {
                Style::default().fg(Color::Yellow).add_modifier(Modifier::BOLD)
            } else {
                Style::default().fg(accent).add_modifier(Modifier::BOLD)
            }
        )
        .use_unicode(true)
        .ratio(ratio)
        .label(if seeking_active { "< seek >" } else { "" });
    f.render_widget(gauge, progress_row[1]);

    f.render_widget(
        Paragraph::new(format!("{:02}:{:02}", tot_min, tot_sec))
            .style(time_style)
            .alignment(Alignment::Left),
        progress_row[2],
    );

    let vol_selected = app.active_panel == ActivePanel::PlayerInfo && app.player_info_index == 0;
    let qual_selected = app.active_panel == ActivePanel::PlayerInfo && app.player_info_index == 1;
    let volume_settings = Paragraph::new(vec![
        Line::from(Span::styled(
            format!("Vol: {}%", app.volume),
            if vol_selected {
                Style::default().fg(Color::Black).bg(Color::Magenta).add_modifier(Modifier::BOLD)
            } else {
                Style::default().fg(Color::DarkGray)
            },
        )),
        Line::from(Span::styled(
            format!("Quality: {}", quality_label(quality)),
            if qual_selected {
                Style::default().fg(Color::Black).bg(Color::Magenta).add_modifier(Modifier::BOLD)
            } else {
                Style::default().fg(Color::DarkGray)
            },
        )),
    ])
        .alignment(Alignment::Right)
        .block(
            Block::default()
                .borders(Borders::ALL)
                .border_style(get_border_style(app, ActivePanel::PlayerInfo))
                .padding(Padding::new(1, 1, 1, 0)),
        );
    f.render_widget(volume_settings, player_bar[2]);
}
