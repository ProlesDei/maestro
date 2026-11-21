# Write tests

Write tests only when the ticket or a person asks for them, and keep them to unit tests.

- Name each test for one behaviour, in given, when, then form: "given an empty file, when it counts words, then it reports 0."
- Test the unit through its public interface, one behaviour per test, beside the repo's tests for that code and in their style.
- Stub what the unit depends on: the network, the clock, files, other services, and other modules when they get in the way. Use a fixed clock and fixed seeds, never sleeps.
- Check each test can fail: break the behaviour it covers, watch it fail, then restore the code. A test that passes either way tests nothing.
