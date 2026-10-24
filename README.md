# ApeMail

A simple IMAP email client, in the spirit of the desktop email clients of
years ago. It is written in Go with a Qt 6 interface using the
[miqt](https://github.com/mappu/miqt) bindings.

![ApeMail showing an account with its Inbox, Drafts, Sent, Archive with a folder for each year, Trash and Projects folders, the messages of the inbox with the unread ones in bold and a flagged one in red, and an HTML message with a list, a table and an attachment](docs/screenshot.png)

## Why ApeMail

Most mail programs grew into something else: a calendar, a chat client, a
news reader, a web browser in a window. ApeMail is a mail client, and only
that, made to be quick, private and careful with your mail, however much of
it you have.

- **Built for big, old mailboxes.** Ten years of mail, a trash of 100,000
  messages: ApeMail only fetches what you look at, asks the server only for
  what has changed, and marks a message read in a folder of 100,000 in less
  than a hundredth of a second. See [Performance](#performance).
- **Fast, and never waiting.** Every account works on several connections
  at once, on every core of your computer, and the window never waits on
  the network or on encryption. Checking for mail goes on while a huge
  folder synchronises.
- **Private by design.** Your accounts, their passwords and the copy of your
  mail are encrypted with your own SSH keys. Images a message links to on
  the internet are not loaded unless you ask, so a sender cannot tell that
  you read it. No account, no cloud, no telemetry: it talks to your mail
  servers, and to the web only for the images you ask for. See
  [Encryption](#encryption).
- **Careful with your mail.** Checking for mail never changes anything on the
  server, opening a message does not mark it read until it is shown, and
  deleted mail goes to the trash. Only deleting from the trash, or deleting a
  folder, removes mail for good, and you are asked first. See
  [Safety](#safety).
- **Simple and familiar.** A menu bar, a bar of large buttons with their
  names under them, the folders on the left, the messages and the message
  on the right. If you used a desktop email client years ago, you already
  know it.
- **No clutter.** No search, threaded conversations, unified inbox,
  templates, calendar, chat or news feeds. Those are what is wrong with
  other clients.
- **IMAP, done properly.** The mail lives on the server, and ApeMail is a
  window onto it that keeps a copy to be quick, never "local folders" that
  only exist on one computer. It speaks the standards servers have: IDLE for
  new mail as it arrives, CONDSTORE to ask only for changes, special-use
  folders, return receipts and delivery status notifications.
- **One small program.** A single compiled binary with a native Qt
  interface, which starts at once and needs nothing beside it.

## Features

**Reading**

- Folders are shown as the server has them, nested, with the inbox, drafts,
  sent mail, archive and trash first, and the number of unread messages
  beside each.
- The message list shows a paperclip by each message with attachments
  (from the message's structure on the server, so nothing is downloaded to
  know), the subject, who it is from (or who it went to, in the sent and
  drafts folders) and the full date and time. Unread messages
  are bold and flagged ones red. Click a heading to sort by it, and drag
  the border between two headings to share the width between them. Above
  it are the folder, with those it is within (as Archive/2026), and how
  many messages it holds.
- Plain text and HTML messages are shown, with their links (web and mail
  addresses written out in the text are made links too, and open in your
  browser or a new message when clicked), the images they carry within
  them, and their attachments, counted among the headers and saved with a
  click (they shade as the mouse passes over them), or all at once with **Save
  All…**, into a folder you choose, where a file already there is never
  written over: the attachment is saved beside it as `name (2).pdf`. Many
  attachments wrap onto rows, and scroll beyond three, rather than widening
  the window.
  An image on the internet is not loaded, and is shown as a small icon where
  it would be, until you press **Load Remote Images** at the bottom right
  of the message's header (or the account is set to load them always, when
  the button shows only if some could not be loaded). Then
  only the images are fetched: no stylesheets, backgrounds or scripts, no
  cookies, and no more than 100 images of 10 MB each.
- HTML is shown by Qt, which reads less of HTML than a web browser does, so
  a message is made fit for it first: what the message hides on a
  computer's screen (`display: none`, in its style or by its stylesheet, as
  a newsletter's preview text, what it shows only on a phone and its
  tracking pixel) is left out, what its stylesheet shows on a computer
  though Outlook is not to see it is shown, and sizes Qt cannot read
  (`width="600px"`, `height="auto"`, or a width in the style) are made
  pixels, as otherwise an image is drawn no height at all and a table
  squeezed into a narrow column.
- **View in Browser**, beside Load Remote Images, shows an HTML message in
  your web browser, as it is meant to look. The browser loads its images
  from the internet, which tells the sender that it was read, but runs none
  of its scripts. The message is not written to a file, which would leave
  it unencrypted on disk: ApeMail serves it to the browser itself, on this
  computer only, at an address no one can guess, for ten minutes.
- New mail in the inbox is seen as it arrives, while ApeMail is running
  (IMAP IDLE). There is no checking every few minutes.
- Only what you look at is fetched, so a mailbox of many years costs nothing
  until you open it. **Get Mail** checks the folder that is selected, and a
  folder is checked when you open it; the others are left alone. A message
  is downloaded only when you open it.
- Large mailboxes stay quick, however many years of mail they hold: see
  [Performance](#performance).
- A message that asks for a **return receipt** asks you, when you open it,
  whether to send one. Either way the message is marked (`$MDNSent`, which
  other email clients use too), so you are asked only once. Your own mail, in the
  sent and drafts folders, never asks. Receipts are not kept in the sent
  folder.

**Looks**

- A light or dark theme, or one that follows the desktop's and changes with
  it (the User interface tab of File > Settings). The icons are ApeMail's
  own, drawn in the colours of the theme, as are the messages read and
  written. The folders of the side bar are each in a colour of their kind,
  filled with a pale wash of it (the inbox blue, drafts purple, sent mail
  green, the archive brown, the trash grey and other folders yellow),
  lighter in the dark theme.

**Writing**

- Write in plain text or HTML (bold, italic, underline, bulleted and
  numbered lists), with a signature for each account.
- A signature can be written in the account's settings, or be a file of text
  or HTML, read each time you write a message so changes to it show at once.
  An HTML signature is turned into text for a message written as text, and
  the images it shows from your computer, as a logo, go with the message.
- Put images in an HTML message, where the cursor is: they go inside the
  message rather than as attachments, and are shown no wider than 600
  pixels.
- Reply, reply to all and forward, with the reply above or below the quote,
  as the account says. Forwarding keeps the attachments, and replies and
  forwards keep the images the message shows.
- File > Close (Escape) closes the window, asking first whether to save a
  message that has not been sent.
- The Edit menu cuts, copies, pastes (also without formatting, in an HTML
  message), deletes and selects in the field or text last clicked in, and
  undoes and redoes. The Format menu has the formatting of the buttons.
- Ask for a **return receipt**, which asks whoever reads the message to say
  so, and for **delivery status notifications**, which ask the mail servers
  to say when the message is delivered or cannot be (Options menu). The
  notifications are asked for only where the outgoing server supports them
  (DSN); otherwise the message is sent as usual.
- The To, Cc and Bcc fields complete what you type from the address book:
  any part of a name or address finds a contact.
- Attach files, and save drafts to the server to finish later. Double-click a
  draft to carry on with it; the drafts it replaces go to the trash.
- Mail that cannot be sent because the server cannot be reached waits in an
  outbox and goes when it can, as do drafts saved without a connection.

**Organising**

- **Archive** moves messages to the archive folder, into a folder for the
  year each was sent in (`Archive/2026`), as other clients do, or a folder
  for each month, or just the archive, as the account says.
- **Delete** moves messages to the trash. In the trash, it deletes them for
  good, after asking.
- After deleting or archiving, the next message is selected and shown, so
  pressing Delete again deletes it too.
- Drag messages onto a folder to move them there.
- Mark messages as read or unread.
- The archive, trash, drafts and sent folders are found by what the server
  marks them as (RFC 6154 special-use), or by their usual names (Archive or
  Archives, Sent Items, Deleted Items, ...), or can be chosen for each
  account. If one is missing it is made on the server the first time it is
  needed. The side bar shows each by what it is for, whatever the server
  calls it, so every account looks the same: an archive called Archives,
  or one you chose, is shown as Archive.
- **New Folder** (the account's menu in the side bar) makes a folder on the
  server, among your own or within another.
- **Delete Folder** (the folder's menu in the side bar) deletes a folder from
  the server, with the folders within it and all their mail, after showing
  how many messages each holds and asking. The inbox and the account's
  sent, drafts, trash and archive folders cannot be deleted.

**Address book**

- The **Address Book** button (or File > Address Book) lists the names and
  addresses of your contacts, to add, change, remove (after asking) and write
  to. Click a column's header to sort by it. Double-click a contact to close
  it and write to them.
- The button beside the sender of a message puts them in the address book,
  or, if the message asks for replies to go elsewhere (Reply-To), whoever
  they go to, as replying does: the button is then beside Reply to.
- Whoever a reply is sent to is put in it too.
- Each address is in it once, whatever its case: one already there is not
  added again, though it is given the name if it had none.

**Accounts**

- **Look Up Settings** finds the servers from the email address, as other
  clients do: from the provider of the domain, the public database of mail
  providers (the ISPDB), the server that handles the domain's mail, and the
  SRV records of the domain.
- Connection security: none, STARTTLS or SSL/TLS. Logging in: a normal
  password, an encrypted password (CRAM-MD5) or a TLS client certificate.
- **Test Connection** for the incoming and outgoing servers, and **Read
  Folders from Server** to choose the folders from.
- Check the inbox for mail when ApeMail starts.
- Load the images messages link to on the internet when they are shown,
  rather than only when asked (off unless you turn it on).
- **Remove Account** (Accounts menu, or the account's menu in the side bar)
  removes it from ApeMail after asking; the mailbox on the server is not
  touched.
- **Update Unread Counts** (Accounts menu, or the account's menu in the side
  bar) asks the server how many messages of every folder are unread, in a
  second or two, without opening any.
- **Synchronise All Folders** fetches the headers of every folder, but no
  messages, so that each opens at once: the inbox first and the trash last.
  The first time, for a large mailbox, it can take a while; it shows how far
  it has got, Get Mail goes on working meanwhile, and the same item stops
  it. Later it is quick, as only what has changed is asked for.

## Performance

ApeMail is made for mailboxes of many years, with folders of 100,000
messages, and stays quick with them. This is how.

### Only what you look at

- Nothing is synchronised in the background. **Get Mail** checks the folder
  that is selected, opening a folder checks it, and the inbox is watched for
  new mail as it arrives (IMAP IDLE). A large folder you never open costs
  nothing.
- A message is downloaded only when you open it. Listing a folder needs only
  its headers.
- When you do want everything up to date, **Synchronise All Folders** does
  it on a connection of its own, and **Update Unread Counts** counts every
  folder without opening any.

### Only what has changed

- With CONDSTORE (RFC 7162), which most servers have, the server keeps a
  count of the changes to each folder. ApeMail keeps it too, and asks only
  for what has changed since. A folder that has not changed costs one
  command; one where a message was marked read costs that message.
- Messages deleted elsewhere are looked for only when the number of
  messages says some have gone.
- Counting unread messages sends every folder's question before waiting for
  any answer, so fifty folders take little longer than one.
- A check that finds nothing new changes nothing on screen or on disk.

### Concurrency

Go makes it natural to do many things at once, and ApeMail does:

- **Every account has its own workers, each with its own connection** to
  the server: one synchronises folders, one does what you ask (opening,
  moving, flagging, sending), and one idles in the inbox. A long
  synchronisation never holds up opening a message or sending one.
- **Synchronising every folder, and counting unread messages, run on
  connections of their own**, so Get Mail goes on working while the trash
  of ten years synchronises. A folder is never synchronised on two
  connections at once, and one that is being already is left to that sync
  rather than waited for.
- **The window never waits.** Network, encryption, decryption, reading and
  saving the local copy all happen away from the UI's thread; the window is
  told when they are done.
- **Saving uses every core.** The indexes of the folders that have changed
  are compressed, encrypted and written at once, each on a goroutine of its
  own, in the background.
- **Sorting uses every core.** For a large folder, what each row is sorted by
  is worked out on every processor at once.
- **Reading a large folder's index holds nothing up.** It is read from disk
  without stopping the account's other work.
- **Nothing is lost when an account stops**, as it does when its settings
  are applied or the program closes: what it was doing is finished first,
  and what you asked of it that had not started is answered as if the
  server could not be reached, so a draft goes to the outbox.
- **A dropped connection is made again.** When the server restarts, or a
  router forgets an idle connection overnight, the next check makes a new
  one rather than failing, and the inbox is checked for what arrived while
  it was gone.

### The local copy

- Each folder has one index of its messages' headers, rather than a file for
  each message, so a folder lists from one file.
- The index is compressed before it is encrypted: that of 100,000 messages
  is under 8 MB rather than 44 MB.
- A change is shown at once and saved a moment later, from a copy, so a run
  of changes is saved once and nothing waits on the disk.
- Each downloaded message is a file of its own, so reading one never touches
  the others.

### The message list

- The list is virtual: it only ever draws the rows on screen, so a folder of
  100,000 messages shows at once.
- When only flags change, as when a message is marked read, just those rows
  are updated; nothing is sorted again.
- A large selection, such as every message of a folder, is kept a run of
  rows at a time rather than a row at a time.

### The message

- Newsletters place things with tables inside tables, a dozen deep, and Qt
  lays out each table again for every pass over the one it is in, so each
  table deeper doubles the time. Tables more than four deep are shown as
  blocks, one below the other, keeping their colours: such a
  newsletter took a minute to show, and now takes a quarter of a second.

### Measured

At 100,000 messages, on an ordinary laptop:

| | Before | Now |
|---|---|---|
| Marking a message read (the list) | 125–750 ms | 8.5 ms |
| Sorting the list by date / by sender | 125 ms / 756 ms | 33 ms / 42 ms |
| Saving the folder's index | 235 ms, on every change | 130 ms, at most every 2 s, in the background |
| The folder's index on disk | 44 MB | under 8 MB |
| Checking an unchanged folder | the flags of every message | one command |

Measure it yourself with `make bench`.

## Keyboard shortcuts

**The main window**

| Keys | Does |
|---|---|
| F5 | Get mail: check the folder that is selected (the account's inbox, if the account is). |
| Ctrl+N | Write a new message. |
| Ctrl+R | Reply. |
| Ctrl+Shift+R | Reply to all. |
| Ctrl+L | Forward. |
| Ctrl+Shift+A | Archive the selected messages. |
| Delete | Move the selected messages to the trash, or, in the trash, delete them for good after asking. |
| Ctrl+A | Select every message in the list. |
| Ctrl+U | Show the source of the message: the message as the server has it, as text. |
| Ctrl+Shift+B | Open the address book. |
| Ctrl+, | Open the settings. |
| Ctrl+I | Show the about box. |
| Ctrl+Q | Quit. |

**Writing a message**

| Keys | Does |
|---|---|
| Ctrl+Return | Send. |
| Ctrl+S | Save as a draft. |
| Ctrl+B, Ctrl+I, Ctrl+U | Bold, italic and underline, in an HTML message. |
| Ctrl+Shift+V | Paste without formatting, in an HTML message. |
| Escape | Close the window (File > Close), asking first whether to save a message that has not been sent. |
| Delete | Remove the selected attachment, in the list of attachments. |

A letter or other character key is never a shortcut on its own, so nothing
happens to your mail from a key pressed by mistake.

## Requirements

ApeMail is developed and tested on Linux. There is a release for macOS on
Apple Silicon (see [macOS app](#macos-app)), but it has not been tested on
macOS by hand yet.

Building needs:

- Go 1.27 or later
- A C++ compiler (GCC or Clang)
- `pkg-config`
- The Qt 6 development files of Qt Widgets and Qt SVG (which draws the icons)

Install them with your distribution's package manager:

```sh
# Arch, CachyOS, Manjaro
sudo pacman -S go gcc pkgconf qt6-base qt6-svg

# Debian, Ubuntu
sudo apt install golang-go build-essential pkg-config qt6-base-dev qt6-svg-dev

# Fedora
sudo dnf install golang gcc-c++ pkgconf-pkg-config qt6-qtbase-devel qt6-qtsvg-devel

# openSUSE
sudo zypper install go gcc-c++ pkgconf-pkg-config qt6-base-devel qt6-svg-devel
```

If your distribution's Go is older than 1.27, install Go from
[go.dev/dl](https://go.dev/dl/) instead.

Check that Qt can be found with:

```sh
pkg-config --modversion Qt6Widgets Qt6SvgWidgets
```

This should print two versions starting with 6.

Running a built binary on another machine only needs the Qt 6 runtime
libraries (`qt6-base` and `qt6-svg` on Arch, `libqt6widgets6` and
`libqt6svgwidgets6` on Debian and Ubuntu, `qt6-qtbase` and `qt6-qtsvg` on
Fedora, `libQt6Widgets6` and `libQt6SvgWidgets6` on openSUSE). The icons
are ApeMail's own, built into the binary, so no icon theme is needed.

## Building

```sh
make build
```

This is the same as:

```sh
go build -ldflags "-s -w" -o apemail .
```

The `-s -w` flags build without the symbol table and debug information, which
roughly halves the size of the binary, to about 35 MB.

The first build compiles the whole Qt binding and takes several minutes,
printing a lot of compiler warnings from the Qt headers which can be ignored.
Later builds use the Go build cache and take a few seconds.

## AppImage

Releases for Linux are an [AppImage](https://appimage.org): one file with
ApeMail and the Qt libraries it needs inside it, which runs without
installing anything.

```sh
chmod +x ApeMail-*-x86_64.AppImage
./ApeMail-*-x86_64.AppImage
```

It is built by a GitHub workflow (`.github/workflows/appimage.yml`), which runs
`packaging/appimage.sh` on Ubuntu 24.04 with Ubuntu's Qt 6.4:

- Pushing a tag that starts with `v` builds it and attaches it to a release of
  that name: `git tag v1.0.0 && git push origin v1.0.0`.
- It can also be run by hand from the Actions tab of the repository (AppImage >
  Run workflow), which leaves the AppImage as an artifact of the run to
  download.

It runs under both X11 and Wayland, using Wayland where the session is one.
To use the other, set `QT_QPA_PLATFORM` to `xcb` or `wayland` when starting
it. Started from an AppImage, ApeMail's entry in the desktop's applications
starts the AppImage.

On GNOME it takes its colours, light or dark, from the desktop, through Qt's
GTK theme plugin, which is in the AppImage. GTK itself is not, and is the
desktop's own.

The C library of the system (glibc) is not put in the AppImage, as it cannot
be reliably: it has to match the system's graphics drivers. It runs on any
system with glibc 2.39 or later: rolling distributions such as CachyOS and
Arch, Ubuntu 24.04, Debian 13, Fedora 40 and anything newer.

`make appimage` runs the same script locally, but an AppImage built on a newer
system only runs on systems at least as new.

## macOS app

Releases for macOS are a disk image, `ApeMail-*-arm64.dmg`, for Macs with
Apple Silicon. Open it and drag ApeMail into Applications. Macs with Intel
processors are not supported.

The app is not signed with an Apple Developer ID or notarised by Apple, so
the first time it is opened macOS says that it cannot check it for malicious
software and will not open it. Either:

- Open System Settings > Privacy & Security, where ApeMail is named
  beneath Security, and choose Open Anyway, or
- take away the mark macOS gives what is downloaded, in Terminal:

  ```sh
  xattr -dr com.apple.quarantine /Applications/ApeMail.app
  ```

Either is only needed once. It is signed ad hoc, which a Mac with Apple
Silicon needs to run it at all.

It is built by a GitHub workflow (`.github/workflows/macos.yml`), which runs
`packaging/macos.sh` on a Mac with Apple Silicon and Homebrew's Qt, when a
`v` tag is pushed, as the AppImage is, or by hand from the Actions tab (macOS
> Run workflow). The script builds the program, makes `ApeMail.app` with its
icon (from `packaging/macos/apemail.png`) and `Info.plist` (from
`packaging/macos/Info.plist`), copies Qt into it with Qt's `macdeployqt`,
checks that nothing in it still uses a library from outside it, signs it,
checks that it starts, and makes the disk image. It needs the macOS the
workflow builds on (macOS 14) or later, which the script works out from what
is in the app and writes into its `Info.plist`.

`make macos` runs the same script on a Mac with Apple Silicon and
`brew install go pkgconf qtbase qtsvg`: the base of Qt and Qt SVG alone,
which is all ApeMail uses, as with the whole of Homebrew's `qt` the plugins
of its other modules go into the app too, with what they use. The icon of the
app is drawn from the program's own, within the margin macOS gives the icons
of the Dock, by `make icons`.

## Running

```sh
./apemail
```

The first time it runs, ApeMail asks for the SSH keys to encrypt your data
with (see [Encryption](#encryption)), and then for your first account. Type
your name and email address and press **Look Up Settings** to fill in the
servers, then enter your passwords.

On Linux, every time it starts, ApeMail adds itself to the desktop's
applications, so that it can be started from the menu and kept in a dock or
sidebar: it writes `~/.local/share/applications/apemail.desktop` and its icon
to `~/.local/share/icons/hicolor/256x256/apps/apemail.png` (or under
`$XDG_DATA_HOME`). The entry starts the copy that ran last, wherever it is.
If the desktop keeps showing an older icon after an update, it has the old
one cached: logging out and in again clears it (on KDE, so does deleting
`~/.cache/icon-cache.kcache`).

The settings are kept where the system keeps the configuration of programs:

| System | Settings file |
|---|---|
| Linux | `~/.config/apemail/settings.json` (or under `$XDG_CONFIG_HOME`) |
| macOS | `~/Library/Application Support/apemail/settings.json` |

They hold the paths of the keys, where the accounts are kept, the
code font the source of a message is shown in and the theme (the User
interface tab of File > Settings; the system's unless one is chosen), the size and
layout of the window (with the widths of the message list's columns, which
every folder shares), the size of the window messages are written in, and
which folders of the side bar are open. They are not encrypted, as they say
where the keys are, so they never name a folder: the open folders are kept by
a hash of their names.

The accounts and the copy of their mail are kept, by default, where the
system keeps the data of programs:

| System | Accounts folder |
|---|---|
| Linux | `~/.local/share/apemail/accounts` (or under `$XDG_DATA_HOME`) |
| macOS | `~/Library/Application Support/apemail/accounts` |

| Path in it | Contents |
|---|---|
| `<id>.json` | An account: who you are, its servers and their passwords, and its settings |
| `<id>/folders.json` | The folders of its mailbox |
| `<id>/f-<hash>/index.json` | The headers of the messages of a folder, which is all the message list needs (compressed, then encrypted) |
| `<id>/f-<hash>/<uid>.eml` | A message that has been downloaded, as the server sent it |
| `<id>/outbox/` | Mail to send and drafts to save once there is a connection |
| `addressbook/contacts.json` | The address book: the name and address of each contact |

Every file is encrypted. The folders are named by a hash of the folder's
name, so not even the names of your folders can be read without your keys.
The copy of the mail is only a cache of what is on the server: it can be
deleted, and is downloaded again.

The accounts folder can be kept elsewhere by choosing its location under
Accounts on the General tab of File > Settings. Changing it moves everything there.

### Encryption

Your accounts, their passwords, the address book and the copy of your mail are encrypted with a
pair of SSH keys, using [age](https://github.com/FiloSottile/age). Choose the
files of the public and the private key (Ed25519 or RSA, such as
`~/.ssh/id_ed25519.pub` and `~/.ssh/id_ed25519`) under Encryption in File >
Settings. The settings hold the paths of the keys, not the keys.

- ApeMail needs the keys to work. Without them it says so, and loads and
  saves nothing.
- If the private key has a passphrase it is asked for when ApeMail starts.
  The SSH agent is not used.
- Choosing another pair encrypts every file again with those.

Keep a copy of the private key: without it the accounts cannot be read. The
mail itself is safe on the server.

### Safety

ApeMail treats your mailbox as yours, not its own:

- Checking for mail only ever reads: folders are opened read-only (IMAP
  `EXAMINE`), and messages are downloaded without being marked read.
- Deleting moves mail to the trash. Only deleting from the trash removes mail
  for good, and you are asked first.
- Deleting a folder asks first, saying how many messages it and every folder
  within it hold. If anything has changed by the time it is done, as mail
  arriving, a message moved out, or a folder made within it, nothing is
  deleted and you are told. A folder being synchronised is not deleted until
  that is done, and the inbox and the folders the account uses are never
  deleted.
- Mail is only ever expunged by its UID, so messages that another client has
  marked as deleted are left alone. A server that cannot expunge particular
  messages (no UIDPLUS) is never asked to expunge at all.
- Before anything is done to messages, ApeMail checks that the server has
  not numbered the folder anew since it was listed, so it can never act on
  the wrong messages.
- Removing an account removes only what is kept on this computer. The
  mailbox on the server is not touched.

## Testing

```sh
make test
```

This runs every test with the race detector and a timeout. The tests of the
mail run against an IMAP server in memory, and check what is on it
afterwards, including that nothing was changed that should not have been.
Synchronising with CONDSTORE is tested against a small IMAP server of the
project's own (`internal/remote/condstoretest`), and sending against an SMTP
server for tests (`internal/send/sendtest`). The interface tests run Qt
offscreen, so they do not need a display. The work done at once is tested
together under the race detector: accounts stopped while every kind of work
is under way, mail sent from several windows at once, the server dropping
every connection, and answers that arrive after the window they were for
has closed.

How a folder of 100,000 messages fares is measured with:

```sh
make bench
```

The screenshot at the top of this file is drawn by a test, from a sample
mailbox, and is made again with:

```sh
make screenshot
```

The icon is `internal/appicon/appicon.png`, built into the program.
