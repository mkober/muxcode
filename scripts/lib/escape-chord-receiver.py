#!/usr/bin/env python3
"""escape-chord-receiver.py — a raw-mode stdin reader that models a TUI key
parser's pending-ESC rule, so an integration script can see the defect a
`cat` receiver cannot (MUX-163: `cat` echoes every byte, so an injected
payload whose first character was fused into a Meta chord still reads back
whole).

    escape-chord-receiver.py <log-file> [--window-ms 500]

A bare ESC is held until the next byte arrives or the window expires
(500 ms by default, node readline's ESCAPE_CODE_TIMEOUT): a byte inside the
window fuses with it into a Meta chord, `ESC [` / `ESC O` opens a CSI/SS3
sequence, and a timeout logs the Escape alone. The rule is applied across
reads, not only inside one chunk — a payload sent by a separate `send-keys`
call still fuses when it lands inside the window, which is what makes this
a model of the composer rather than of tmux.

Every event is one line in the log file, mirrored to stdout so a pane
capture shows the same thing:

    key <name>       a plain key: the character itself, or Space, Enter,
                     Tab, BSpace, C-<x>, Escape (a bare ESC that timed out)
    chord M-<name>   ESC fused with the key that followed it
    seq <text>       a CSI/SS3 escape sequence (arrows, function keys)

Ctrl-C (byte 3 — raw mode delivers it as a key) or EOF ends the run. With
a non-tty stdin the reader skips raw mode, so a pipe can drive it:
`(printf '\\033'; sleep 0.1; printf 'hi') | escape-chord-receiver.py log`.
"""

import argparse
import os
import select
import sys
import termios
import tty

ESC = 0x1B

NAMED_KEYS = {
    0x09: "Tab",
    0x0A: "LF",
    0x0D: "Enter",
    0x20: "Space",
    0x7F: "BSpace",
    ESC: "Escape",
}


def utf8_length(lead):
    if lead < 0x80:
        return 1
    if lead >> 5 == 0b110:
        return 2
    if lead >> 4 == 0b1110:
        return 3
    if lead >> 3 == 0b11110:
        return 4
    return 1


def key_name(data):
    """Name the key encoded by data (one key: a control byte, an ASCII
    character, or one UTF-8 sequence)."""
    b = data[0]
    if b in NAMED_KEYS:
        return NAMED_KEYS[b]
    if b < 0x20:
        return "C-" + chr(b + 0x60)
    if b < 0x80:
        return chr(b)
    return data.decode("utf-8", errors="replace")


def is_csi_final(b):
    return 0x40 <= b <= 0x7E


class Receiver:
    def __init__(self, fd, log, window):
        self.fd = fd
        self.log = log
        self.window = window
        self.buf = b""
        self.pending_esc = False

    def emit(self, line):
        self.log.write(line + "\n")
        self.log.flush()
        sys.stdout.write(line + "\r\n")
        sys.stdout.flush()

    def read_more(self, timeout):
        """Append the next chunk to buf; False on timeout, EOFError on EOF."""
        ready, _, _ = select.select([self.fd], [], [], timeout)
        if not ready:
            return False
        data = os.read(self.fd, 4096)
        if not data:
            raise EOFError
        self.buf += data
        return True

    def take_key(self):
        n = utf8_length(self.buf[0])
        while len(self.buf) < n:
            if not self.read_more(self.window):
                break
        key, self.buf = self.buf[:n], self.buf[n:]
        return key

    def take_sequence(self):
        """Consume a CSI (`[` params final) or SS3 (`O` final) sequence that
        follows an ESC; an incomplete one is reported as far as it got."""
        opener = self.buf[:1]
        self.buf = self.buf[1:]
        body = b""
        while True:
            if not self.buf and not self.read_more(self.window):
                break
            b = self.buf[0]
            body += self.buf[:1]
            self.buf = self.buf[1:]
            if opener == b"O" or is_csi_final(b):
                break
        return "ESC" + (opener + body).decode("ascii", errors="replace")

    def fuse(self):
        """Resolve a pending ESC against the byte now at the head of buf."""
        self.pending_esc = False
        if self.buf[:1] in (b"[", b"O"):
            self.emit("seq " + self.take_sequence())
            return
        self.emit("chord M-" + key_name(self.take_key()))

    def consume(self):
        if self.buf[0] == ESC:
            self.buf = self.buf[1:]
            self.pending_esc = True
            return
        key = self.take_key()
        self.emit("key " + key_name(key))
        if key == b"\x03":
            raise KeyboardInterrupt

    def run(self):
        while True:
            if self.pending_esc:
                if not self.buf and not self.read_more(self.window):
                    self.pending_esc = False
                    self.emit("key Escape")
                    continue
                self.fuse()
                continue
            if not self.buf:
                self.read_more(None)
            self.consume()


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("log_file")
    parser.add_argument("--window-ms", type=int, default=500,
                        help="how long a bare ESC waits for a follower (default 500)")
    args = parser.parse_args()

    fd = sys.stdin.fileno()
    saved = termios.tcgetattr(fd) if os.isatty(fd) else None
    if saved is not None:
        tty.setraw(fd)
    try:
        with open(args.log_file, "w") as log:
            Receiver(fd, log, args.window_ms / 1000.0).run()
    except (KeyboardInterrupt, EOFError):
        pass
    finally:
        if saved is not None:
            termios.tcsetattr(fd, termios.TCSADRAIN, saved)


if __name__ == "__main__":
    main()
