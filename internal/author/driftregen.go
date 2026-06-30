package author

// DriftRegenPrompt builds the scoped system+user prompts for regenerating ONE diff
// block whose patch no longer applies. The model is asked to return a single fresh
// unified diff (text), not to call submit_playbook.
func DriftRegenPrompt(currentFile, stalePatch string) (sys, user string) {
	sys = "You previously produced a patch that no longer applies to its target file " +
		"(the file changed). Produce a FRESH unified diff that achieves the same intent " +
		"against the CURRENT file content. Output ONLY the unified diff (--- /+++ /@@ …), " +
		"no prose, no fences."
	user = "The stale patch (no longer applies):\n\n" + stalePatch +
		"\n\nThe CURRENT content of the target file:\n\n" + currentFile +
		"\n\nReturn the corrected unified diff."
	return sys, user
}
