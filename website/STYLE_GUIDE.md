# Documentation Style Guide

Files
- Filenames should be prefixed with random 6-digit identifier generated via `new.sh` script
- URLs for SDK-specific pages should be in the form `/sdk/[language]/[number]/[label]`
- URLs for API- or CLI-specific pages should be in the form `/[api|cli]/[number]/[label]`
- URLs for non-SDK pages should be in the form `/[number]/[label]`

Page titles
- Page titles should be in Word Case
- For guides and tutorials, start each title with a verb e.g. `Create Pipeline...`, `Deploy App...`

Page sections
- Section headings should be in Sentence case

Code
- SDK example code should be stored in `sdk/snippets` subdirectory
  - Subdirectory name = docs filename that embeds its snippets
  - Separate each code snippet further into its own subdirectory if required so it can be run standalone

Text
- Avoid using `you`, `we`, `our` and other personal pronouns. Alternatives are (e.g. instead of `this is where you will deploy the application`):
  - Rewrite the sentence using passive voice e.g. `this is where the application will be deployed`   
  - Rewrite the sentence to personify the subject e.g. `the application will be deployed here`
  - Rewrite the sentence using active voice and a verb e.g. `deploy the application to...`
