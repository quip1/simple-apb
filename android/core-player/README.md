# core-player

Playback for the Android app (brief §7.2, ADR-0040).

- `PlaybackService` is the one Media3 `MediaLibraryService`. The app, the notification and lock
  screen, headset and Bluetooth buttons, and Android Auto all go through its
  session.
- `BookTimeline` maps book-global positions to (file, offset) and answers chapter questions.
- `PlaybackRepository` loads books, reports progress (409 conflicts), and handles bookmarks.
- `PlayerConnection` is the app's `MediaController` and holds book-level helpers.
- `PlaybackSettings` and `PlaybackStore` hold per-device settings and per-book records: speed,
  local position (the last sync time lives in the Room mirror).
- `SleepTimer`, `ShakeDetector` and `HeadsetPresses` are plain logic and unit-tested.

- `EffectsProcessor` / `EffectsDsp`: the audio effects (boost with limiter, voice EQ, mono,
  balance) in ExoPlayer's sink (ADR-0043).
- `BrowseTree` and `CoverProvider`: the Android Auto browse tree, search, and covers the car
  host can read.
