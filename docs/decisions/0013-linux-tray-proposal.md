# 0013: Linux tray companion

Date: 2026-10-27. Status: design approved by the user; implementation plan awaits review.

The user uses Waybar and wants a Grove status icon through a general Linux
desktop protocol. [Approved spec 000014](../spec/000014-linux-freedesktop-tray-design.md)
defines a separate `grove-tray` companion using StatusNotifierItem and D-BusMenu,
with offline status from the existing shared inspection service.

A separate process can remain visible when the daemon is stopped and keeps
graphical-session dependencies out of the headless daemon. This is preferred to
embedding a tray icon in `groved` or requiring a panel-specific script.

One profile per invocation, polling every two seconds, the menu contents, and
optional validated systemd user-service controls were author proposals accepted
with the written design.
Starting or quitting the companion does not change daemon lifecycle. Service
actions require explicit target configuration and never change sync mode,
conflict policy, or recovery intent. The
[paired plan](../implementation-plans/000014-linux-freedesktop-tray-plan.md)
is prepared for review; native execution is the user's preserved preference.
