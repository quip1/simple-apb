# Android Auto checklist (Desktop Head Unit)

Phase 3 is accepted when Android Auto passes these checks (brief §15, §16). They follow
Google's quality guidelines for car media apps and what the app promises in ADR-0043. Run them
with the Desktop Head Unit (DHU) before a release, and in a real car when you can.

## Setting up the DHU

1. In Android Studio's SDK Manager, under **SDK Tools**, install **Android Auto Desktop Head
   Unit Emulator**. It lands in `<sdk>/extras/google/auto/`.
2. On the phone, install the Android Auto app (on Android 10 and later it's built in: Settings →
   Connected devices → Android Auto).
3. Turn on its developer mode: in Android Auto's settings, tap **Version** ten times.
4. From the ⋮ menu, choose **Developer settings** and turn on **Unknown sources**. Debug and
   GitHub builds aren't from Google Play, so Android Auto hides them without it.
5. From the ⋮ menu, choose **Start head unit server**.
6. Connect the phone by USB and run:
   ```bash
   adb forward tcp:5277 tcp:5277
   ```
7. Start `desktop-head-unit` (`.exe` on Windows) from the DHU folder. A car screen opens on the
   computer.

Sign in to your server in the app on the phone first, and download one book.

## The checks

Write the date, the phone, the Android version and the app build at the top of your notes. Mark
each check passed or failed, and add a note for a failure.

### Browsing
- [ ] Simple APB is listed among the car's media apps, with its icon and name.
- [ ] The top level shows Continue listening, Recently added, Downloaded, Collections, Series
      and Authors.
- [ ] Books show as a grid with their covers. Started books show how far along they are, and
      finished books are marked finished.
- [ ] Series and Authors open to lists of books, and a series is in reading order.
- [ ] No list takes text input, and nothing asks to type anything.
- [ ] A long list scrolls smoothly, and long titles are cut off cleanly rather than overlapping.

### Playing
- [ ] Tapping a book plays it from where you left off: the same position as on the phone.
- [ ] The now-playing screen shows the cover, the title and the author.
- [ ] Play/pause, back 30 s, forward 30 s, previous chapter and next chapter all work, and their
      icons are clear at a glance.
- [ ] The car's steering-wheel buttons (the DHU's next/previous keys) skip by the configured
      amount, not to the next file.
- [ ] Speed, sleep timer and the audio effects set on the phone stay in effect.
- [ ] Progress made in the car shows up on the phone, and on the web after a minute.

### Voice
Use the DHU's microphone button, with the computer's microphone.
- [ ] "Play <a book's title> on Simple APB" plays that book.
- [ ] "Play <part of an author's name> on Simple APB" plays one of their books.
- [ ] A title that isn't in the library gets a spoken or shown "not found". It doesn't crash,
      and it doesn't play something random.
- [ ] "Play Simple APB" with no title resumes the last book.

### Offline and errors
- [ ] Turn the phone's network off: the Downloaded list still opens, a downloaded book plays,
      and a book that isn't downloaded shows an error message (not an endless spinner).
- [ ] Turn the network back on: the position made offline reaches the server.
- [ ] Sign out on the phone: Android Auto shows a message to sign in on the phone, not an empty
      or broken list.
- [ ] In local-only mode (no server), books from folders on this device can be browsed and play.

### Interruptions
- [ ] A navigation prompt (start route guidance in Maps on the phone) pauses the book, or lowers
      it if "lower the volume" is chosen in the app's settings. It resumes after.
- [ ] A phone call pauses the book; it resumes when the call ends.
- [ ] Disconnecting the DHU (close it, or pull the cable) pauses playback on the phone.
- [ ] Connecting again offers to resume the last book.

### The phone while driving
- [ ] With the DHU connected, the phone's own screen shows nothing that needs reading or typing
      to keep the book playing.
- [ ] Car mode on the phone (if it opens) matches what's playing in the car.

## When a check fails

Note the steps in an issue labelled `android-auto`, with the DHU's console output. Media session
problems usually show in `adb logcat -s MediaSessionService PlaybackService`.
