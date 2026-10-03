package cmd

// builtinsStub declares the names that the sandbox puts in the
// globals of the code. Without it, Pyright and Pylance report
// Score and EvaluationResult as undefined, and an agent that
// trusts the editor adds imports that the sandbox does not need.
const builtinsStub = `# The names that the code evaluator sandbox gives evaluate.py
# without an import. Pyright and Pylance read this file, so the
# code type-checks here as it runs in Oodle. Do not edit it.
from oodle_eval.runtime import (
    EvaluationContext as EvaluationContext,
    EvaluationResult as EvaluationResult,
    ObservationContext as ObservationContext,
    Score as Score,
    Scores as Scores,
    TraceContext as TraceContext,
)

ctx: EvaluationContext
`

// pyrightConfig checks the user's code only. oodle_eval/ stays
// importable (the directory is the root) but is not a source to
// check: it is the reference that the server runs.
const pyrightConfig = `{
  "include": ["evaluate.py", "shared"],
  "exclude": ["oodle_eval", ".oodle"],
  "executionEnvironments": [{ "root": "." }],
  "pythonVersion": "3.12",
  "typeCheckingMode": "basic"
}
`

const workspaceReadme = "# Oodle code evaluator\n\n" +
	"This directory is one Oodle code evaluator template, written by\n" +
	"`oodle genai templates pull`. Edit the files, test, then push.\n\n" +
	"## Layout\n\n" +
	"| Path | What it is | Edit? |\n" +
	"|------|------------|-------|\n" +
	"| `evaluate.py` | The code. `evaluate(ctx)` returns an `EvaluationResult`. | Yes |\n" +
	"| `template.yaml` | Name, settings (`params`), `libraryPins`, `scoreType` and other fields. | Yes |\n" +
	"| `shared/<name>.py` | The shared libraries of the instance. Import one as `shared.<name>`. A new file here is a new library. | Yes |\n" +
	"| `oodle_eval/` | The `oodle_eval` library that the sandbox runs. For reading only. | No |\n" +
	"| `__builtins__.pyi` | The names the sandbox gives the code, for type checks. | No |\n" +
	"| `pyrightconfig.json` | Type checks for Pyright and Pylance. | If you want |\n" +
	"| `.oodle/` | What was pulled: the template id and the library versions. | No |\n\n" +
	"## Rules\n\n" +
	"- `Score`, `EvaluationResult`, `Scores` and `EvaluationContext` need no\n" +
	"  import. Write `def evaluate(ctx: EvaluationContext):` for completion.\n" +
	"- Use absolute imports only: `from shared.acme_text import clean`,\n" +
	"  not `from .acme_text import clean`. A relative import fails in the sandbox.\n" +
	"- The code can import only these modules: `json`, `sys`, `math`, `re`,\n" +
	"  `statistics`, `collections`, `string`, `datetime`, `decimal`, `fractions`,\n" +
	"  `itertools`, `functools`, `operator`, `copy`, `textwrap`, `unicodedata`,\n" +
	"  `difflib`, `hashlib`, `hmac`, `base64`, `binascii`, `struct`, `enum`,\n" +
	"  `typing`, `dataclasses`, `abc`, `numbers`, `random`, `bisect`, `heapq`,\n" +
	"  `array`, `pprint`, `contextlib`, `csv`, `tomllib`, `html`, `ipaddress`,\n" +
	"  `urllib.parse`, `oodle_eval` and `shared`.\n" +
	"- The names `shared` and `oodle_eval` are reserved: do not make a\n" +
	"  module or a variable with these names.\n" +
	"- These builtins are removed: `exec`, `eval`, `compile`, `open`,\n" +
	"  `getattr`, `setattr`, `delattr`, `globals`, `locals`, `vars`,\n" +
	"  `breakpoint`, `exit`, `quit`, `memoryview`.\n" +
	"- Each item has 5 seconds and 128 MB. Limit the work on long text.\n" +
	"- A library file name is a lower-case Python identifier, such as\n" +
	"  `acme_text.py`.\n\n" +
	"## Commands\n\n" +
	"```bash\n" +
	"oodle genai library                        # the oodle_eval reference\n" +
	"oodle genai templates test . --trace <trace-id>   # run on a real trace\n" +
	"oodle genai templates push . --dry-run     # validate, print the plan\n" +
	"oodle genai templates push .               # save to Oodle\n" +
	"oodle genai templates validate -d .        # check without saving\n" +
	"oodle genai templates pull <id> . --force  # get the server's version\n" +
	"```\n\n" +
	"`push` validates first, then creates or updates the changed shared\n" +
	"libraries, then saves the template. It refuses to replace a library\n" +
	"that someone changed on the server after your pull, and a change to\n" +
	"a pinned library file that is older than the latest version.\n\n" +
	"`pull --force` keeps your changes: it stops and lists the files that\n" +
	"you changed since the last pull or push. `--discard-local` replaces\n" +
	"them, and the changes are lost.\n"
