## Useful Prompts

Prompt for generating test scenarios:
```
Improve pkg/shell/tests scenarios coverage by taking inspiration from pkg/shell/yash_posix_tests

Notes:
- avoid duplicate test coverage, if you encounter duplicate scenarios, remove or merge them
- create as many new scenarios as possible (no limit), of course they must be valuable
- if some tests fail, keep in mind it's possible that pkg/shell implementation is wrong, and it's fine to fix the implementation
```

Prompt to avoid disabling test_against_local_shell when possible:
```
In pkg/shell/tests, for each scenarios with test_against_local_shell is disabled,
examine why test_against_local_shell is disabled.

test_against_local_shell is usually disabled because the restricted shell doesn't behave as bash

BUT if it's disabled because of a bug in restricted shell (making it behave differently than bash),
then the restricted shell implementation must be fixed
```

Good sources of POSIX shell test scenarios:
- [yash POSIX shell test suite](https://github.com/magicant/yash/tree/trunk/tests)
