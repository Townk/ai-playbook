## broken-build

A deliberately broken fake project — ask ai-playbook to fix me (see ch.09).

`build.sh` fails because it reads `version.txt`, but this repo ships `VERSION` instead.
The fix is obvious: either create `version.txt` or correct the script. Ch.09 walks through
authoring a playbook via `ai-playbook create` to perform the fix.
