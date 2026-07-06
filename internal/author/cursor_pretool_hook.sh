#!/bin/sh
# ai-playbook cursor preToolUse hook — the ENFORCED builtin-tool allowlist for
# the FULL authoring path. cursor-agent runs FULL authoring in AGENT mode (its
# MCP tools are refused in --mode ask/plan), and agent mode also exposes
# cursor's builtin write/shell tools, which execute headlessly under -p with no
# per-command gate (live-verified). This hook fires before EVERY tool call and
# permits ONLY our MCP tools ("MCP:<tool>"); every builtin (Shell/Write/Read/…)
# is denied. Registered with failClosed:true, so a crash/timeout/garbage output
# also BLOCKS — the deny is fail-closed in every direction.
#
# tool_name is the FIRST occurrence on the wire (before tool_input), so the
# first-match extraction cannot be spoofed by a fake "tool_name" embedded in a
# tool argument. An empty/absent name falls through to deny.
input=$(cat)
name=$(printf '%s' "$input" | grep -oE '"tool_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n 1 | sed -E 's/.*"([^"]*)"$/\1/')
case "$name" in
  MCP:*)
    printf '{"permission":"allow"}\n'
    ;;
  *)
    printf '%s\n' '{"permission":"deny","user_message":"ai-playbook: builtin tool blocked (authoring uses only the ai-playbook MCP tools).","agent_message":"Builtin tools are disabled in this authoring session. Use only the ai-playbook MCP tools: run, ask, remember, submit_playbook."}'
    ;;
esac
