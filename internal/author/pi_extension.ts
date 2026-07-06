// pi_extension.ts — the ai-playbook tool transport for the pi harness.
//
// piHarness.ToolTransport writes this file into the per-invocation transport
// dir (splicing the session's tools socket path into SOCKET_PATH below) and
// attaches it via `--extension <path>`. It registers the SAME four tools the
// MCP adapter (internal/mcpserver) exposes to claude — run / remember / ask /
// submit_playbook — each forwarding to the session's tools backend over its
// unix socket (internal/tools' newline-framed JSON RPC), and renders each
// reply with the same text internal/mcpserver.renderResult produces, so the
// model-visible tool surface is identical across harnesses.
//
// Deliberately dependency-free: tool parameters are plain JSON-schema objects
// (pi's tool loop compiles and enforces raw JSON schema — typebox is only the
// convenience layer), and the only import is node:net, so the file loads from
// any temp path without a node_modules resolution dance. pi validates the
// arguments against these schemas BEFORE execute() and returns a validation
// failure to the model as an error tool result (the re-ask loop); a thrown
// error here (backend unreachable) is reported the same way.
import * as net from "node:net";

const SOCKET_PATH = "__AI_PLAYBOOK_SOCKET__";

// callBackend sends one newline-framed JSON request to the tools backend and
// resolves with the decoded one-line reply (the internal/tools wire protocol).
// Transport failures reject, which pi reports to the model as a tool error.
function callBackend(req: Record<string, unknown>): Promise<Record<string, any>> {
	return new Promise((resolve, reject) => {
		const sock = net.createConnection(SOCKET_PATH);
		let buf = "";
		let settled = false;
		const fail = (err: unknown) => {
			if (settled) return;
			settled = true;
			sock.destroy();
			reject(new Error("ai-playbook tools backend unreachable: " + String(err)));
		};
		sock.on("connect", () => {
			sock.write(JSON.stringify(req) + "\n");
		});
		sock.on("data", (chunk: Buffer) => {
			buf += chunk.toString("utf8");
			const nl = buf.indexOf("\n");
			if (nl < 0) return;
			settled = true;
			sock.destroy();
			try {
				resolve(JSON.parse(buf.slice(0, nl)));
			} catch (err) {
				reject(new Error("ai-playbook tools backend sent a malformed reply: " + String(err)));
			}
		});
		sock.on("error", fail);
		sock.on("close", () => fail("connection closed without a reply"));
	});
}

// text wraps a string as the tool-result content pi expects.
function text(s: string): { content: { type: "text"; text: string }[] } {
	return { content: [{ type: "text", text: s }] };
}

export default function (pi: any) {
	pi.registerTool({
		name: "run",
		label: "Run",
		description:
			"Run a command in the user's real interactive shell (their cwd and environment) and return its stdout, stderr, and exit code. Use this — NOT your own shell — so commands execute in the user's environment. Keep them read-only or idempotent.",
		promptSnippet: "Run a command in the user's real interactive shell",
		parameters: {
			type: "object",
			properties: {
				cmd: {
					type: "string",
					description: "the command line to run in the user's real interactive shell (their cwd and environment)",
				},
				id: {
					type: "string",
					description:
						"optional short id; exports APB_OUT_<id>/APB_ERR_<id>/APB_EXIT_<id> so a later call can reference this command's output",
				},
			},
			required: ["cmd"],
		},
		async execute(_toolCallId: string, params: { cmd: string; id?: string }) {
			const res = await callBackend({ tool: "run", cmd: params.cmd, id: params.id ?? "" });
			if (res.error) {
				return text("error: " + res.error);
			}
			let out = `exit: ${res.exit ?? 0}`;
			if (res.out) out += "\n--- stdout ---\n" + res.out;
			if (res.err) out += "\n--- stderr ---\n" + res.err;
			return text(out);
		},
	});

	pi.registerTool({
		name: "remember",
		label: "Remember",
		description:
			"Save a durable, distilled fact for future requests, classified by kind: " +
			"'system' (machine/tooling truths), 'user' (who the user is or prefers), " +
			"'environment' (this project's setup), or 'topic' (a domain-specific lesson, " +
			"with a topic name) — classify by how closely the fact is tied to the topic at " +
			"hand. Never save secrets or raw environment dumps.",
		promptSnippet: "Save a durable, distilled fact for future requests",
		parameters: {
			type: "object",
			properties: {
				kind: {
					type: "string",
					description:
						"classifies the fact by how closely it is tied to the topic at hand: 'system' for machine/tooling truths, 'user' for who the user is or prefers, 'environment' for this project's setup, 'topic' for a domain-specific lesson (requires topic). One of: system, user, environment, topic.",
				},
				topic: {
					type: "string",
					description: "the topic name; required when kind=topic, invalid for any other kind",
				},
				fact: {
					type: "string",
					description: "a durable, distilled fact to save for future requests; never secrets or env dumps",
				},
				projectRoot: {
					type: "string",
					description:
						"optional project root override; only valid when kind is environment or topic (defaults to the session's project root)",
				},
			},
			required: ["kind", "fact"],
		},
		async execute(_toolCallId: string, params: { kind: string; topic?: string; fact: string; projectRoot?: string }) {
			const res = await callBackend({
				tool: "remember",
				kind: params.kind,
				topic: params.topic ?? "",
				fact: params.fact,
				projectRoot: params.projectRoot ?? "",
			});
			if (res.error) {
				return text("error: " + res.error);
			}
			return text(res.ok ? "saved" : "not saved");
		},
	});

	pi.registerTool({
		name: "ask",
		label: "Ask",
		description: "Ask the user a question and return their answer. The only way to get input from the user.",
		promptSnippet: "Ask the user a question and return their answer",
		parameters: {
			type: "object",
			properties: {
				prompt: { type: "string", description: "the question to ask the user" },
				type: { type: "string", description: "input type: free|line|confirm|choose (default free)" },
			},
			required: ["prompt"],
		},
		async execute(_toolCallId: string, params: { prompt: string; type?: string }) {
			const res = await callBackend({ tool: "ask", prompt: params.prompt, type: params.type ?? "" });
			if (res.unavailable) {
				return text(res.error ?? "interactive ask not available in this context");
			}
			return text(res.answer ?? "");
		},
	});

	pi.registerTool({
		name: "submit_playbook",
		label: "Submit Playbook",
		description:
			"Submit the FINISHED playbook as structured data. This is your FINAL action and your deliverable — do NOT write the playbook as markdown in your reply; call this tool with the playbook object instead. The host renders the markdown. If it returns a validation error, fix the object and call submit_playbook again.",
		promptSnippet: "Submit the finished playbook as structured data",
		// The playbook object schema — the extension-side mirror of
		// internal/draft.Playbook (its json/jsonschema struct tags). The backend
		// re-validates with draft.Validate, so this schema is the model-facing
		// contract while the Go side stays authoritative.
		//
		// The block between the SCHEMA markers is STRICT JSON (quoted keys, no
		// trailing commas — JSON is valid JS): the Go parity test
		// (TestPiExtension_SubmitPlaybookSchemaParity) extracts it verbatim and
		// compares it against the schema derived from draft.Playbook — the same
		// derivation the MCP transport serves claude — so a draft.Playbook change
		// fails the tests until this mirror is updated. Keep it JSON.
		parameters: /* AI_PLAYBOOK_SCHEMA_BEGIN */ {
			"type": "object",
			"properties": {
				"title": {
					"type": "string",
					"description": "the playbook name; rendered as the H1 title. A short imperative phrase, e.g. 'Restore the Gradle wrapper'."
				},
				"intro": {
					"type": "string",
					"description": "optional lead prose before the first section (markdown)"
				},
				"sections": {
					"type": "array",
					"description": "the ordered sections of the playbook; at least one",
					"items": {
						"type": "object",
						"properties": {
							"heading": {
								"type": "string",
								"description": "the section heading, rendered as '## <heading>'"
							},
							"content": {
								"type": "array",
								"description": "ordered, heterogeneous list of prose and code items; render in order",
								"items": {
									"type": "object",
									"properties": {
										"kind": {
											"type": "string",
											"description": "one of: text, callout, code"
										},
										"text": {
											"type": "string",
											"description": "for kind=text or kind=callout: literate markdown prose"
										},
										"admonition": {
											"type": "string",
											"description": "for kind=callout: the callout type — one of note|tip|important|warning|caution (default note); selects the icon + color"
										},
										"lang": {
											"type": "string",
											"description": "for kind=code: the language/interpreter — bash|zsh|sh|python|diff|console|…"
										},
										"code": {
											"type": "string",
											"description": "for kind=code: the block content"
										},
										"id": {
											"type": "string",
											"description": "for kind=code: optional stable id for value-passing; we auto-assign when omitted"
										},
										"needs": {
											"type": "array",
											"items": { "type": "string" },
											"description": "for kind=code: ids of earlier blocks this one depends on"
										},
										"rollback": {
											"type": "string",
											"description": "for kind=code: the id of the block this one rolls back"
										},
										"static": {
											"type": "boolean",
											"description": "for kind=code: true if the block is non-runnable (console output / illustrative)"
										},
										"file": {
											"type": "string",
											"description": "for a NEW file: the relative path; the block body is the file's full content (use a diff block to EDIT an existing file)"
										},
										"from": {
											"type": "string",
											"description": "for kind=code: id of an earlier shell/run block whose captured stdout feeds this block's stdin (e.g. a python block reading sys.stdin); implies a needs= dependency on that id; only shell/run blocks may set this, and only a shell/run block may be the target"
										},
										"timeout": {
											"type": "string",
											"description": "for kind=code: optional per-block execution ceiling, Go duration (e.g. '15m') — declare ONLY for steps known to run long (installs, first captures, large downloads); omit otherwise (default 10m)"
										}
									},
									"required": ["kind"]
								}
							}
						},
						"required": ["heading", "content"]
					}
				},
				"verify": {
					"type": "object",
					"description": "the final outcome-check command, rendered as the {id=verify} block. Include it for a troubleshooting/fix playbook.",
					"properties": {
						"lang": {
							"type": "string",
							"description": "the language/interpreter for the verify command"
						},
						"code": {
							"type": "string",
							"description": "the verify command content"
						},
						"needs": {
							"type": "array",
							"items": { "type": "string" },
							"description": "ids the verify depends on (usually the fix block)"
						},
						"from": {
							"type": "string",
							"description": "id of an earlier shell/run block whose captured stdout feeds the verify command's stdin; same rules as a code block's from="
						}
					},
					"required": ["lang", "code"]
				},
				"meta": {
					"type": "object",
					"description": "classification + front-matter metadata for the saved playbook",
					"properties": {
						"description": {
							"type": "string",
							"description": "a one-line imperative summary of what the playbook does"
						},
						"category": {
							"type": "string",
							"description": "a coarse category, e.g. 'Android / build' or 'macOS / networking'"
						},
						"tags": {
							"type": "array",
							"items": { "type": "string" },
							"description": "keywords for search"
						},
						"project_bound": {
							"type": "boolean",
							"description": "true if this playbook is specific to a project/working directory; false for a general how-to that applies anywhere"
						},
						"env": {
							"type": "array",
							"description": "environment variables the playbook relies on (local resources, secrets) — declare each with name + why so a reader on another machine knows what to set",
							"items": {
								"type": "object",
								"properties": {
									"name": {
										"type": "string",
										"description": "the variable name, e.g. ANDROID_SDK_ROOT"
									},
									"why": {
										"type": "string",
										"description": "one line on what it is / why the playbook needs it"
									}
								},
								"required": ["name"]
							}
						}
					},
					"required": ["description", "project_bound"]
				}
			},
			"required": ["title", "sections", "meta"]
		} /* AI_PLAYBOOK_SCHEMA_END */,
		async execute(_toolCallId: string, params: Record<string, unknown>) {
			const res = await callBackend({ tool: "submit_playbook", playbook: params });
			if (res.error) {
				return text("validation error: " + res.error);
			}
			return text(res.ok ? "saved" : "not saved");
		},
	});
}
