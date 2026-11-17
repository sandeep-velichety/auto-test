# UI review and decisions

Question: does Testplane need a different UI, judged against other API test tools?

**Decision: keep the current layout and add the specific things the other tools do better.** A redesign is not justified; the gaps were in individual features, not in the structure.

## What was compared

Published 2026 comparisons of Postman, Insomnia, Bruno, Katalon and SoapUI/ReadyAPI were read for what users praise and complain about in each interface. This was a reading of reviews, not hands-on use of each product.

| Tool | What reviewers like | What they dislike |
| --- | --- | --- |
| Postman | does everything: collection runner, environments, gRPC, CI runner with JUnit reports | interface feels cluttered, with a learning curve |
| Insomnia | clean, focused interface; fetches the schema and offers it while you write a request | weaker automation and reporting |
| Bruno | tests live as files in the Git repository next to the code; simple CLI, including running one request | fewer team features |
| Katalon | point-and-click test building for testers who do not code | heavy for API-only work |
| SoapUI / ReadyAPI | thorough reporting for regulated teams | dated interface |

Sources: [Sematext](https://sematext.com/blog/best-8-postman-alternatives-reviewed-and-compared/), [yrkan.com](https://yrkan.com/blog/api-tools-comparison-2025/), [dev.to comparison](https://dev.to/_d7eb1c1703182e3ce1782/postman-vs-insomnia-vs-bruno-best-api-client-in-2026-1pf7), [apidog.com](https://apidog.com/api-testing-tools), [requestly.com](https://requestly.com/?p=71047), [toolradar.com](https://toolradar.com/compare/postman-vs-insomnia).

## Where Testplane already matched

- Tests as plain files that can live in Git (Bruno's main selling point).
- A headless runner with JUnit output for pipelines (Postman's Newman, Insomnia's Inso, Bruno's CLI).
- Form-based authoring without code (Katalon), with YAML available for those who prefer it.
- An uncluttered screen: one application, four tabs, one scenario open at a time.
- Environments, inherited auth and client certificates per host (Postman).

## What was missing, and what was done

| Gap | Seen in | Decision | Status |
| --- | --- | --- | --- |
| Discover what a service offers instead of typing method names | Insomnia's schema fetch, Postman's gRPC client | gRPC step lists services and methods and fills in a request template | built |
| Try one request while writing it | every interactive client; Bruno's single-request run | "Run to this step" button on each step; partial runs are not stored in history | built |
| See at a glance what passed | collection runners | last result shown on each scenario in the list and on each step after a run | built |
| Start from something that already exists | import from cURL in several tools | "Paste cURL" creates an HTTP step | built |
| Move a test between workspaces as one file | Postman collection export | "Download YAML" exports a scenario with the auth profiles and certificate sets it uses; "Import" reads it back | built |
| Find a scenario in a long list | all | filter box by name or tag (appears once there are more than four scenarios) | built |
| Data-driven runs (one scenario over a table of inputs) | Postman runner, ReadyAPI | worth doing next; changes how scenarios are written, so it needs its own design | not built |
| Import from OpenAPI or a Postman collection | several | useful for onboarding existing suites; not needed for new ones | not built |
| Trends and dashboards across runs | ReadyAPI, Postman monitors | history with filters exists; charts can come once teams have weeks of runs | not built |
| Dark theme | most | cosmetic | not built |

## Why not a redesign

- The complaint reviewers repeat about the most capable tool is clutter. Testplane's screen has few elements, and each new feature above was added inside the step or list it belongs to rather than as a new area.
- Testers work scenario by scenario. The current split (list on the left, one scenario on the right, results under it) matches that and did not get in the way of any feature added so far.
- The two things most likely to force a layout change later are data-driven runs and dashboards. Both are listed above as not built; the layout should be revisited when one of them is designed.
