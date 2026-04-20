# Roadmap

## Features

- Effort/thinking selector for model
- ~~Support locally installed agent skills~~
- ~~Create new agent from agent selector~~
- ~~Message queueing - allow posting messages when waiting for a response~~
- Need a way to amend pending message (including deleting it) by pressing 'up'
- Improve stats table (could render using markdown renderer?)
- Show session name as well as agent name
- ~~Replace static `_` placeholder when awaiting final response message with simple character animation~~
- Session browser — navigate and restore previous conversations (not just recent history on startup)
- Persistent model switching — persist `/model` selection across restarts per agent
- Completion notification — terminal bell (or optional system notification) when a long response finishes while the window is unfocused
- Skill discovery — `/skills search <term>` to browse and install skills from the ClawHub registry without leaving the TUI

## Bugs

- Different indentation for messages from 'You' and the agent
- Can't select/copy from TUI when window is focussed
- ~~newline key doesn't work~~ (alt+enter works)
- Long user messages seem to be truncated
- Timestamps appearing at the start of user messages in message history
- When triggering the `/skills` command don't send the output as a user message to the model
