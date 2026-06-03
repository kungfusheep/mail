# mail

A keyboard-first terminal mail client built with [glyph](https://github.com/kungfusheep/glyph).

`mail` is a local-first IMAP/SMTP client with a cached mailbox model, vim-flavoured distraction-free compose editor, rich preview rendering, attachment handling, and a quiet three-pane TUI.

<img width="2427" height="1496" alt="image" src="https://github.com/user-attachments/assets/34c44313-147b-44a5-b3ad-af8d630c26b2" />


## Status

This is pre-release software. It is useful locally, but the configuration and release story are still being shaped.

## Features

- Cached IMAP mailbox with inbox, sent, drafts, trash, spam, archive, and labels.
- Thread list with grouped conversations, attachment chips, sender colour hints, unread/star/read actions, undo, snooze, and local rules.
- Preview pane with structured letter-style headers, attachment rows, link targets, calendar/date detection, footer dimming, and focused scrollbar.
- Compose view with SMTP send, drafts, signatures, reply, reply-all, forward, inline reply, local attachments, and visible send feedback.
- Vim-style editor bindings with search, motions, text objects, visual mode, registers, clipboard support, spell suggestions, and `gm*` document maps.
- Omnibox command palette for mail actions, theme switching, movement, and compose/editor helpers.
- Theme support, including the bundled dark/light and MFD palettes.

## Install

Build from source:

```sh
go build -o mail .
```

Run:

```sh
./mail
```

The app writes logs to:

```text
/tmp/mail.log
```

## Configuration

Create `~/.config/mail/config.json`:

```json
{
  "server": "imap.gmail.com:993",
  "smtp_server": "smtp.gmail.com:587",
  "email": "you@example.com",
  "password": "app-password"
}
```

Settings such as the active theme and compose signature are stored in:

```text
~/.config/mail/settings.json
```

## Useful Keys

- `?` opens the keyboard help overlay.

- `j` / `k` move within the focused pane.
- `h` / `l` move focus left and right.
- `tab` moves to the next pane.
- `enter` opens the selected folder, thread, message, link, or attachment.
- `/` searches mail.
- `c` composes a new message.
- `r` replies.
- `ra` replies all from the omnibox.
- `fwd` forwards from the omnibox.
- `a` archives.
- `d` deletes.
- `z` snoozes until tomorrow morning.
- `s` toggles star.
- `e` toggles read/unread.
- `u` undoes the latest thread action.

## Compose Editor

Compose uses a vim like embedded editor

Useful normal-mode commands include:

- `/`, `?`, `n`, `N`, `*`, `#` for search.
- `ciw`, `diw`, `yiw`, visual mode, registers, yank/paste, and common vim motions.
- `z=` for spell suggestions when `aspell` is available.
- `gmh`, `gml`, `gmq`, `gmc`, `gmt`, `gmd`, `gmP`, `gms` for block maps.
- `gmf`, `gm"`, `gm'`, ``gm` ``, `gm(`, `gm[`, `gm{`, `gm<` and matching closing variants for text maps.


<img width="2422" height="1495" alt="image" src="https://github.com/user-attachments/assets/c2f324f4-9aac-4a96-aa3a-6e8f1b865504" />

<img width="2556" height="1542" alt="image" src="https://github.com/user-attachments/assets/74b18c71-c88f-4652-bba7-ff217e6e1175" />

<img width="2552" height="1528" alt="image" src="https://github.com/user-attachments/assets/11303957-92c6-4d6e-a214-484270839eca" />

## Themes

A bunch of colour themes are included, accessed via the omnibox

<img width="2424" height="1460" alt="image" src="https://github.com/user-attachments/assets/c0219740-4c15-47f2-b6e9-70d67dceb859" />


## Release Assets

Built binaries should be attached to GitHub Releases rather than committed into the repository.

Example:

```sh
go build -o dist/mail-darwin-arm64 .
gh release create v0.1.0 dist/mail-darwin-arm64 --title v0.1.0
```
