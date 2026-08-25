package bus

// builtinGraphJSON holds the built-in graph templates, keyed by name.
// These are tier 3 of template resolution (project > user > builtin) and
// every entry must pass Graph.Validate() — pinned by TestBuiltinGraphTemplatesValidate.
//
// ${intent} in node messages is replaced with the graph run's intent
// argument at execution time (Phase 4).
var builtinGraphJSON = map[string]string{
	"build-test-review": `{
  "name": "build-test-review",
  "description": "Standard build, test, review pipeline as a reusable subgraph",
  "start": "build",
  "nodes": [
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the latest changes on this branch"}
  ],
  "edges": [
    {"from": "build", "to": "test"},
    {"from": "test", "to": "review"}
  ]
}`,

	"coding-pr": `{
  "name": "coding-pr",
  "description": "Implement, build/test with capped fix loop, review, then human-gated commit and PR",
  "start": "implement",
  "nodes": [
    {"id": "implement", "type": "spawn", "role": "edit", "message": "Implement: ${intent}"},
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "fix", "type": "spawn", "role": "edit", "message": "Fix the reported build or test failure for: ${intent}"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the latest changes on this branch"},
    {"id": "ship-gate", "type": "wait_human", "message": "Approve commit and PR creation for: ${intent}"},
    {"id": "commit", "type": "send", "role": "commit", "action": "commit", "message": "Stage and commit the changes for: ${intent}"},
    {"id": "pr", "type": "send", "role": "commit", "action": "commit", "message": "Create a PR for the current branch"}
  ],
  "edges": [
    {"from": "implement", "to": "build"},
    {"from": "build", "to": "test"},
    {"from": "build", "to": "fix", "outcome": "failure"},
    {"from": "test", "to": "review"},
    {"from": "test", "to": "fix", "outcome": "failure"},
    {"from": "fix", "to": "build", "max_iterations": 3},
    {"from": "review", "to": "ship-gate"},
    {"from": "ship-gate", "to": "commit"},
    {"from": "commit", "to": "pr"}
  ]
}`,

	"story-lifecycle": `{
  "name": "story-lifecycle",
  "description": "Requirements draft, human approval, implement with fix loop, review, human-gated commit and PR",
  "start": "requirements",
  "nodes": [
    {"id": "requirements", "type": "send", "role": "plan", "action": "update-docs", "message": "Draft a requirements doc for: ${intent}"},
    {"id": "req-gate", "type": "wait_human", "message": "Approve the requirements before implementation starts"},
    {"id": "implement", "type": "spawn", "role": "edit", "message": "Implement per the requirements doc: ${intent}"},
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "fix", "type": "spawn", "role": "edit", "message": "Fix the reported build or test failure"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the changes on this branch"},
    {"id": "ship-gate", "type": "wait_human", "message": "Approve commit and PR for: ${intent}"},
    {"id": "commit", "type": "send", "role": "commit", "action": "commit", "message": "Stage and commit: ${intent}"},
    {"id": "pr", "type": "send", "role": "commit", "action": "commit", "message": "Create a PR for the current branch"}
  ],
  "edges": [
    {"from": "requirements", "to": "req-gate"},
    {"from": "req-gate", "to": "implement"},
    {"from": "implement", "to": "build"},
    {"from": "build", "to": "test"},
    {"from": "build", "to": "fix", "outcome": "failure"},
    {"from": "test", "to": "review"},
    {"from": "test", "to": "fix", "outcome": "failure"},
    {"from": "fix", "to": "build", "max_iterations": 3},
    {"from": "review", "to": "ship-gate"},
    {"from": "ship-gate", "to": "commit"},
    {"from": "commit", "to": "pr"}
  ]
}`,

	"research-critique": `{
  "name": "research-critique",
  "description": "Fan out two independent research drafts, join, critique, synthesize",
  "start": "outline",
  "nodes": [
    {"id": "outline", "type": "send", "role": "research", "action": "research", "message": "Outline research angles for: ${intent}"},
    {"id": "draft-a", "type": "spawn", "role": "research", "message": "Draft answer A for: ${intent}"},
    {"id": "draft-b", "type": "spawn", "role": "research", "message": "Draft answer B from an independent angle for: ${intent}"},
    {"id": "gather", "type": "join", "join": "all"},
    {"id": "critique", "type": "send", "role": "review", "action": "review", "message": "Critique the two research drafts for: ${intent}"},
    {"id": "synthesize", "type": "send", "role": "research", "action": "research", "message": "Synthesize the drafts and critique into a final answer for: ${intent}"}
  ],
  "edges": [
    {"from": "outline", "to": "draft-a"},
    {"from": "outline", "to": "draft-b"},
    {"from": "draft-a", "to": "gather"},
    {"from": "draft-b", "to": "gather"},
    {"from": "gather", "to": "critique"},
    {"from": "critique", "to": "synthesize"}
  ]
}`,

	"deploy-verify": `{
  "name": "deploy-verify",
  "description": "Deploy, run a verification invocation, watch logs",
  "start": "deploy",
  "nodes": [
    {"id": "deploy", "type": "send", "role": "deploy", "action": "deploy", "message": "Run the deployment and report changes"},
    {"id": "verify", "type": "send", "role": "run", "action": "run", "message": "Run a verification invocation and report output"},
    {"id": "watch", "type": "send", "role": "watch", "action": "watch", "message": "Tail the deployment logs and report errors"}
  ],
  "edges": [
    {"from": "deploy", "to": "verify"},
    {"from": "verify", "to": "watch"}
  ]
}`,
}
