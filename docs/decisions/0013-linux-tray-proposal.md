# 0013: Linux tray companion

Date: 2026-10-27. Status: approved design/plan implemented; independent review findings verified.

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
is implemented natively. Named SVG icons plus embedded PNG pixmaps avoid theme
dependence. The pinned godbus v5.2.4 library provides native Go D-Bus support
under BSD-2-Clause. Waybar and actual disposable service controls are verified;
a second independent host and login/autostart remain untested.


The independent implementation review of `ac1ec50` found four important edge
cases, all regression-backed and fixed: unvalidated busy startup remains
attention despite historical records; job/result correlation survives unit-state
polling; cancellation during initial service observation exits gracefully; Stop
relies on fresh matched-service eligibility, while Start retains available,
inactive-profile requirements. No critical/minor findings remain.
