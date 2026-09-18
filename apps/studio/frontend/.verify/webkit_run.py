#!/usr/bin/env python3
"""Loads the harness in WebKitGTK -- the engine Wails actually uses on Linux.

Offscreen on purpose: this must not steal a window, the pointer or focus from
whoever is using the desktop. The page drives itself and reports over HTTP.
"""
import sys
import gi

gi.require_version("Gtk", "3.0")
gi.require_version("WebKit2", "4.1")
from gi.repository import GLib, Gtk, WebKit2  # noqa: E402

URL = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:9345/.verify/index.html"

win = Gtk.OffscreenWindow()
win.set_default_size(1400, 900)
view = WebKit2.WebView()
settings = view.get_settings()
settings.set_enable_developer_extras(True)
settings.set_enable_write_console_messages_to_stdout(True)
win.add(view)
win.show_all()

print("webkit:", WebKit2.get_major_version(), WebKit2.get_minor_version(), WebKit2.get_micro_version(), flush=True)


def on_load(_view, event):
    if event == WebKit2.LoadEvent.FINISHED:
        print("load finished", flush=True)


view.connect("load-changed", on_load)
view.load_uri(URL)

GLib.timeout_add_seconds(90, Gtk.main_quit)
Gtk.main()
