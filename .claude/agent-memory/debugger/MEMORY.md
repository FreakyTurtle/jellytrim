# debugger memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.

- 2026-09-28: "database disk image is malformed" on the dev stack came from host-side access to a WAL database used by a container over a macOS bind mount, not from JellyTrim. Check how the database was touched before suspecting the store code.
