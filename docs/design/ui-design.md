# Web UI design

2026-10-31. The visual reference is [ui-concept.png](ui-concept.png), generated with the built-in Image Gen tool as an implementation reference. Real media always comes from the user's catalog; generated thumbnail content is never seeded into the app.

## Design system

- Canvas: true white `#ffffff`; sidebar `#f4f6f3`; main text `#192922`; accent `#285442`; selected navigation `#e1e9e1`; fine neutral dividers.
- Desktop: 244px fixed sidebar, open main canvas, 34px horizontal content gutters, header breadcrumb and directory action, 38px primary task heading, 80px search bar, 3-column photographic gallery. These dimensions were refined against the reference at its native 1505×1045 size.
- Type: Georgia serif for lowercase vgalary wordmark; Korean system sans-serif for app controls and headings. Controls have explicit 13–15px text, filenames 13px and paths 12px.
- Icons: restrained 1.6–1.8px outline icons (search, folder, list, settings, crop corners, chevrons, download, play, archive). No decorative icon rows.
- Gallery: subtle photograph corners, approximately 1.62:1 crop, filenames and single-line paths below the image, no outer panel border. Keyboard-accessible detail trigger; unavailable previews use an honest media placeholder.
- Reusable controls: forest-green primary button, neutral bordered secondary button, open table/list sections, consistent fields and small labels, subtle status indicators.
- Mobile: horizontal top brand/navigation, one-column gallery, stacked search/filter controls, full-screen detail dialog. Preserve the white canvas and typography hierarchy.

## Required behavior

Search and filter the real API, open media details, select video timestamps, copy source paths, download originals, and request server-side export. Directories support add, scan, forced scan and removal. Jobs expose live status, counters, issues, cancel and retry. Settings edit the complete persisted configuration, API-key environment references and frame-change threshold, and run explicit connection checks. Domain mode adds a password sign-in surface with the same visual system.

The four screens share this reference's component family. Copy for the search screen follows the concept; other labels extend it only for these requested functions. The library view can show actual indexed media before a search; counts and statuses always derive from API data.
