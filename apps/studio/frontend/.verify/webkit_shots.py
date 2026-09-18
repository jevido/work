#!/usr/bin/env python3
"""webkit_run.py, plus the screenshot host the harness's shot() waits for.

The page asks for a picture by setting window.__shot to a name; this takes it,
writes it under the output directory and sets window.__shot back to null, which
is what unblocks the driver. Without a host answering, shot() waits forever --
so this is the runner to use for any drive script that calls it.

Offscreen on purpose: this must not steal a window, the pointer or focus from
whoever is using the desktop.

  python3 .verify/webkit_shots.py <url> [outdir]
"""
import os
import sys
import gi

gi.require_version("Gtk", "3.0")
gi.require_version("WebKit2", "4.1")
from gi.repository import GLib, Gtk, WebKit2  # noqa: E402

URL = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:9345/.verify/changes.html"
OUT = sys.argv[2] if len(sys.argv) > 2 else "../.tmp-verify"
os.makedirs(OUT, exist_ok=True)

win = Gtk.OffscreenWindow()
win.set_default_size(1400, 900)
view = WebKit2.WebView()
settings = view.get_settings()
settings.set_enable_developer_extras(True)
settings.set_enable_write_console_messages_to_stdout(True)
win.add(view)
win.show_all()

print("webkit:", WebKit2.get_major_version(), WebKit2.get_minor_version(), WebKit2.get_micro_version(), flush=True)

# One shot at a time: the page waits for each to be acknowledged before asking
# for the next, so this only guards against a poll landing mid-snapshot.
busy = False


def js(source):
    view.run_javascript(source, None, None)


def wanted(_view, result):
    global busy
    try:
        value = view.run_javascript_finish(result).get_js_value()
    except Exception:
        return
    name = value.to_string() if not value.is_null() else "null"
    if name in ("null", "undefined", ""):
        return

    busy = True

    def written(_v, res):
        global busy
        try:
            view.get_snapshot_finish(res).write_to_png(os.path.join(OUT, f"{name}.png"))
            print("shot:", name, flush=True)
        except Exception as err:
            print("shot failed:", name, err, flush=True)
        # Clearing it is the page's go-ahead, so it happens after the write.
        js("window.__shot = null")
        busy = False

    view.get_snapshot(WebKit2.SnapshotRegion.VISIBLE, WebKit2.SnapshotOptions.NONE, None, written)


def poll():
    if not busy:
        view.run_javascript("window.__shot", None, wanted)
    return True


def on_load(_view, event):
    if event == WebKit2.LoadEvent.FINISHED:
        print("load finished", flush=True)
        GLib.timeout_add(150, poll)


view.connect("load-changed", on_load)
view.load_uri(URL)

GLib.timeout_add_seconds(120, Gtk.main_quit)
Gtk.main()
