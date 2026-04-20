# Roadmap

## Features

- Effort/thinking selector for model
- ~~Support locally installed agent skills~~
- ~~Create new agent from agent selector~~
- ~~Message queueing - allow posting messages when waiting for a response~~
- Need a way to amend pending message (including deleting it) by pressing 'up'
- Improve stats table (could render using markdown renderer?)
- Show session name as well as agent name

## Bugs

- Different indentation for messages from 'You' and the agent
- Can't select/copy from TUI when window is focussed
- shift+enter for new line doesn't work
- Long user messages seem to be truncated
- Timestamps appearing at the start of user messages in message history
- When triggering the `/skills` command don't send it as a user message to the model
