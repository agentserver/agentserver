## Task scheduling (`schedule_task`)

For any recurring task, use `schedule_task`. Tasks persist across sessions and restarts, and support the pre-task `script` hook described below.

To inspect or change existing tasks, use `list_tasks` (returns one row per series with the stable id) and `update_task` / `cancel_task` / `pause_task` / `resume_task`. Prefer `update_task` over cancel + reschedule.

Frequent recurring tasks — more than a few times a day — consume API credits and can risk account restrictions. You can add a `script` that runs first, and you will only be called when the check passes.

### How it works

1. Provide a bash `script` alongside the `prompt` when scheduling
2. When the task fires, the script runs first
3. Script returns: `{ "wakeAgent": true/false, "data": {...} }`
4. If `wakeAgent: false` — nothing happens, task waits for next run
5. If `wakeAgent: true` — the agent receives the script's data + prompt and handles

### Always test your script first

Before scheduling, run the script directly to verify it works.

### When NOT to use scripts

If a task requires your judgment every time (daily briefings, reminders, reports), skip the script — just use a regular prompt. Do not attempt sentiment analysis or advanced NLP in scripts.
