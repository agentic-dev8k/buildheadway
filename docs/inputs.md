# Inspect saved agent inputs

BuildHeadway already saves each external agent invocation's prompt as a private `.prompt.txt` file beside its logs. `inputs` makes those actual inputs visible from the terminal, so you can inspect the instructions, work order, platform details and evidence that the agent received.

```sh
buildheadway inputs
buildheadway inputs --run RUN_ID
buildheadway inputs --run RUN_ID PROMPT_NAME.prompt.txt
buildheadway inputs --run RUN_ID --json --offset 0 --bytes 16000 PROMPT_NAME.prompt.txt
```

Choose the exact filename from the listing. Flags precede the filename. In the interactive app, use `/inputs` or `/inputs PROMPT_NAME.prompt.txt`; use the CLI with `--run RUN_ID` for an older saved goal. Inspection reads existing files without changing checkpoints, consuming a model call, discovering agents or requiring authentication. Simulated and undispatched invocations may have no prompt file.

Without `--offset`, a named input shows its last 16,000 bytes. Use `--offset 0` to start at the beginning and repeat with the JSON `next_offset` while `more` is true. Each read is limited to 1 MiB. Offsets count bytes, so a multibyte character can span page boundaries; consult the original file for byte-exact reconstruction of multibyte text. Files can grow between calls. The JSON shape matches [log inspection](usage.md#read-earlier-process-output), including `offset`, `next_offset`, `more` and the full file's `bytes`.

Only regular, contained `.prompt.txt` files are eligible. Arbitrary paths, traversal and symlink escapes are rejected. `logs` continues to accept log files only. The command does not create additional prompt copies or export them anywhere. Saved prompts can include project content and requirements; inspect them locally and review their contents before sharing.

This is evidence of the input supplied to the external agent, not proof that the agent followed it. Keep using task history, executed checks, acceptance coverage and senior review to assess outcomes. See [automatic instructions](prompts.md) and [harness and recovery](harness.md).
