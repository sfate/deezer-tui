use super::*;

fn test_app() -> App {
    let (tx, _rx) = mpsc::unbounded_channel();
    let mut app = App::new(Config::default(), tx);
    app.current_playlist_id = Some("__flow__".to_string());
    app
}

fn track(id: &str, title: &str, artist: &str) -> (String, String, String) {
    (id.to_string(), title.to_string(), artist.to_string())
}

#[test]
fn handle_down_moves_from_navigation_to_playlists_at_settings_row() {
    let mut app = test_app();
    app.playlists = vec![("p1".to_string(), "Playlist".to_string())];
    app.nav_state.select(Some(4));

    app.handle_down();

    assert_eq!(app.active_panel, ActivePanel::Playlists);
    assert_eq!(app.playlist_state.selected(), Some(0));
}

#[test]
fn handle_up_moves_from_empty_playlists_to_navigation_settings() {
    let mut app = test_app();
    app.active_panel = ActivePanel::Playlists;
    app.playlists.clear();

    app.handle_up();

    assert_eq!(app.active_panel, ActivePanel::Navigation);
    assert_eq!(app.nav_state.selected(), Some(4));
}

#[test]
fn handle_down_on_main_advances_through_track_list_and_stops_at_end() {
    let mut app = test_app();
    app.active_panel = ActivePanel::Main;
    app.current_tracks = vec![track("1", "One", "A"), track("2", "Two", "B")];
    app.main_state.select(Some(0));

    app.handle_down();
    assert_eq!(app.main_state.selected(), Some(1));

    app.handle_down();
    assert_eq!(app.main_state.selected(), Some(2));

    app.handle_down();
    assert_eq!(app.main_state.selected(), Some(2));
}

#[test]
fn handle_up_on_main_returns_to_search_from_first_track_row() {
    let mut app = test_app();
    app.active_panel = ActivePanel::Main;
    app.current_tracks = vec![track("1", "One", "A")];
    app.main_state.select(Some(0));

    app.handle_up();

    assert_eq!(app.active_panel, ActivePanel::Search);
}

#[test]
fn handle_right_cycles_player_panel_into_player_info() {
    let mut app = test_app();
    app.active_panel = ActivePanel::Player;
    app.player_button_index = 4;

    app.handle_right();

    assert_eq!(app.active_panel, ActivePanel::PlayerInfo);
}

#[test]
fn handle_left_on_main_cycles_search_categories_and_resets_selection() {
    let mut app = test_app();
    app.active_panel = ActivePanel::Main;
    app.showing_search_results = true;
    app.search_category = SearchCategory::Tracks;
    app.main_state.select(Some(2));

    app.handle_left();
    assert_eq!(app.search_category, SearchCategory::Artists);
    assert_eq!(app.main_state.selected(), Some(0));

    app.handle_left();
    assert_eq!(app.search_category, SearchCategory::Playlists);

    app.handle_left();
    assert_eq!(app.search_category, SearchCategory::Tracks);
}

#[test]
fn switch_search_category_right_rotates_categories_only_in_search_results_main_panel() {
    let mut app = test_app();
    app.active_panel = ActivePanel::Main;
    app.showing_search_results = true;
    app.search_category = SearchCategory::Tracks;
    app.main_state.select(Some(3));

    app.switch_search_category_right();
    assert_eq!(app.search_category, SearchCategory::Playlists);
    assert_eq!(app.main_state.selected(), Some(0));

    app.switch_search_category_right();
    assert_eq!(app.search_category, SearchCategory::Artists);

    app.active_panel = ActivePanel::Player;
    app.switch_search_category_right();
    assert_eq!(app.search_category, SearchCategory::Artists);
}

#[test]
fn load_flow_tracks_populates_queue_and_returns_first_track_when_autoplaying() {
    let mut app = test_app();
    let tracks = vec![track("1", "One", "A"), track("2", "Two", "B")];

    let first = app.load_flow_tracks(tracks.clone(), true);

    assert_eq!(first.as_deref(), Some("1"));
    assert_eq!(app.current_tracks, tracks);
    assert_eq!(app.queue_tracks, tracks);
    assert_eq!(
        app.queue,
        vec!["One - A".to_string(), "Two - B".to_string()]
    );
    assert_eq!(app.queue_index, Some(0));
    assert!(app.is_playing);
}

#[test]
fn append_flow_tracks_skips_duplicates_and_autoplays_first_new_track() {
    let mut app = test_app();
    app.load_flow_tracks(vec![track("1", "One", "A"), track("2", "Two", "B")], true);

    let result = app.append_flow_tracks(
        vec![
            track("2", "Two", "B"),
            track("3", "Three", "C"),
            track("4", "Four", "D"),
        ],
        true,
    );

    assert_eq!(result.appended_count, 2);
    assert_eq!(result.autoplay_track_id.as_deref(), Some("3"));
    assert_eq!(app.queue_tracks.len(), 4);
    assert_eq!(app.current_tracks.len(), 4);
    assert_eq!(app.queue_index, Some(2));
    assert_eq!(app.queue[2], "Three - C");
    assert_eq!(app.queue[3], "Four - D");
}

#[test]
fn append_flow_tracks_skips_duplicates_within_the_same_batch() {
    let mut app = test_app();
    app.load_flow_tracks(vec![track("1", "One", "A")], true);

    let result = app.append_flow_tracks(
        vec![
            track("2", "Two", "B"),
            track("2", "Two", "B"),
            track("3", "Three", "C"),
        ],
        true,
    );

    assert_eq!(result.appended_count, 2);
    assert_eq!(result.autoplay_track_id.as_deref(), Some("2"));
    assert_eq!(app.queue_tracks.len(), 3);
    assert_eq!(
        app.queue_tracks.iter().map(|(id, _, _)| id.as_str()).collect::<Vec<_>>(),
        vec!["1", "2", "3"]
    );
}

#[test]
fn append_flow_tracks_without_autoplay_still_reports_appended_tracks() {
    let mut app = test_app();
    app.load_flow_tracks(vec![track("1", "One", "A"), track("2", "Two", "B")], true);

    let result = app.append_flow_tracks(vec![track("3", "Three", "C")], false);

    assert_eq!(result.appended_count, 1);
    assert_eq!(result.autoplay_track_id, None);
    assert_eq!(app.queue_tracks.len(), 3);
    assert_eq!(app.current_tracks.len(), 3);
    assert_eq!(app.queue_index, Some(0));
    assert!(app.is_playing);
    assert!(app.is_flow_queue);
}

#[test]
fn append_flow_tracks_does_not_overwrite_non_flow_page_tracks() {
    let mut app = test_app();
    app.load_flow_tracks(vec![track("1", "One", "A"), track("2", "Two", "B")], true);
    app.current_playlist_id = Some("__home__".to_string());
    app.current_tracks = vec![track("10", "Home Track", "Home Artist")];

    let result = app.append_flow_tracks(vec![track("3", "Three", "C")], false);

    assert_eq!(result.appended_count, 1);
    assert_eq!(app.current_tracks, vec![track("10", "Home Track", "Home Artist")]);
    assert_eq!(app.queue_tracks.len(), 3);
}

#[test]
fn should_load_more_flow_only_on_last_queued_flow_track_even_if_playlist_id_changes() {
    let mut app = test_app();
    app.load_flow_tracks(vec![track("1", "One", "A"), track("2", "Two", "B")], true);

    assert!(!app.should_load_more_flow());

    app.queue_index = Some(1);
    assert!(app.should_load_more_flow());

    app.flow_loading_more = true;
    assert!(!app.should_load_more_flow());

    app.flow_loading_more = false;
    app.current_playlist_id = Some("__home__".to_string());
    assert!(app.should_load_more_flow());
}

#[test]
fn should_not_load_more_flow_when_playlist_id_is_stale_but_queue_is_not_flow() {
    let mut app = test_app();
    app.current_playlist_id = Some("__flow__".to_string());
    app.queue_tracks = vec![track("1", "One", "A")];
    app.queue_index = Some(0);
    app.is_flow_queue = false;

    assert!(!app.should_load_more_flow());
}

#[test]
fn non_flow_tracks_load_keeps_flow_cursor_when_flow_queue_is_still_active() {
    let mut app = test_app();
    app.is_flow_queue = true;
    app.flow_next_index = 24;
    app.current_playlist_id = Some("__home__".to_string());

    if app.current_playlist_id.as_deref() != Some("__flow__") && !app.is_flow_queue {
        app.flow_next_index = 0;
    }
    app.current_tracks = vec![track("10", "Home Track", "Home Artist")];

    assert_eq!(app.flow_next_index, 24);
    assert_eq!(app.current_tracks, vec![track("10", "Home Track", "Home Artist")]);
}
