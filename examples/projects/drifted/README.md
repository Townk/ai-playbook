## drifted

A fake project that has drifted from its expected state. The `settings.conf` file ships
with `timeout = 99`, but ch.05's patch expects `timeout = 30` — so `git apply --check`
fails on open, triggering the drift region demo.

Used in chapter 05 to demonstrate drift detection, resolve-manually, and regenerate.
