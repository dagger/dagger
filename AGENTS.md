Where we're *going* is a robust, elegant and cohesive model for our cache implementation based on e-graphs,
with all persistent caching going through purely dagql and NOT buildkit.

# CRUCIAL CODE STYLE AND COLLABORATION GUIDANCE

1. Use a HARD CUTOVER approach and never implement backward compatibility. We *don't* want to find utterly minimal changes that hack together fixes in cases where there are larger changes that make the whole system more cohesive, understandable and robust. However, we also don't refactor for the sake of refactoring. If a simple change is the most elegant in the context of the full system, that's the right change to make. For each proposed change, examine the existing system and redesign it into the most elegant solution that would have emerged if the change had been a foundational assumption from the start
2. We hate abstraction for the sake of aesthetics. We don't factor out separate functions that are only called in 1 or a few places. We don't care about making code look like prose; we care about making code easy to *truly* understand in fine detail. Abstraction can be needed to not copy-paste extremely dense code all over the place, and in other similar situations. But we only pursue it once proven necessary.
3. We are EMPIRICAL. When we have theories, we write+run tests to verify them, including with lots of extra debugging logs, metrics and other data when needed. It's easy to remove debugging helpers later, so we're not afraid of using them.
4. Don't take any of my ideas+suggestions as gospel. Consider them, do they make sense? Is there something I'm missing? Is there a better way? I'm right a lot, but far from always! And the same is true of you. We're a team and together greater than the sum of our parts.
5. We are working on a LARGE project with lots of moving parts that can't always move all at once. There are times where existing comments and unit tests are OUT OF DATE and can thus be SELECTIVELY disregarded. Unit tests and comments are NOTgospel. Known updated ones are a useful tool for understanding, but must be followed with caution.

Also, if I refer to a WHITEBOARD.md file where we collaborate on notes, TODOs, task progress, etc. you can find it in ./scratch/WHITEBOARD.md

# DEBUGGING

./skills/cache-expert/references/debugging.md HAS CRUCIAL INFORMATION WHICH MUST BE READ AND NEVER FORGOTTEN!

Additionally, you should make heavy use of subagents when running tests and parsing their output in order to keep your context more open. 

# COMMIT MESSAGES

Rules for commit messages: 

Please include a detailed commit message on the problem, the solution we went with and implementation details. Imagine that you are an LLM reading the commit message with no context on the change, include everything you'd need to get back up to speed.

• Commit message formatting rule (important):
  - Never put literal `\n` in `git commit -s -m "..."` expecting real newlines.
  - Use a real multiline message via either:
    1) multiple `-m` flags (one per paragraph), or
    2) a message file + `git commit -s -F <file>` (preferred for long messages).

Preferred pattern:
  1. Write the message to a temp file with actual line breaks.
  2. Run `git commit -s --amend -F /tmp/commitmsg.txt` (or `git commit -F /tmp/commitmsg.txt`).
  3. Verify with `git log -1 --format=%B`.


