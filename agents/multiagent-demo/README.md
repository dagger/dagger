# Multi Agent Demo

This is a demo of a function using multiple LLMs to complete a (silly) task.

The first LLM, a coding oriented model, is tasked with a coding assignment: write a go program to get the weather in San Fransisco.

The second LLM, a chat oriented model, is given the output of the program written by the first, and asked if I should wear a jacket today.


```
✔ demo --coder-model qwen2.5-coder:14b --chat-model llama3.2 2m58s
It's cold today in San Francisco, so I recommend wearing a jacket.
│🧑 You are an expert go programmer. You have access to a workspace. Use the read, write, build tools to complete the following assignment:
│ ┃
│ ┃               write a program called weather
│ ┃               that retrieves current weather in San Francisco from wttr.in
│ ┃               and prints a short report about the temperature and precipitation
│ ┃               to the console
│ ┃
│ ┃ DO NOT USE THE CONTAINER TOOL
│ ┃
│ ┃ Don't stop until your code builds.
│
│ ✔ ToyWorkspace.build: String! 2.3s
│
│ ✔ ToyWorkspace.write(content: "// main.go\npackage main\n\nimport (\n\t\"fmt\"\n\t\"io/ioutil\"\n\t\"net/http\"\n)\n\nfunc getWeather() (string, error) {\n\tresp, err := http.Get(\"http:
│ ✔ .build: String! 4.0s
│
│ ✔ ToyWorkspace.write(content: "module weather\n\ngo 1.17", path: "go.mod"): ToyWorkspace! 2.1s
│ ✔ .build: String! 1m9s
│
│🤖 Great! Your code has successfully built. You can now run the  weather  program to retrieve and print the current weather in San Francisco from wttr.in.
│ ┃
│ ┃ If you need any further assistance, feel free to ask!
│
│🧑
│ ┃           You have access to a file describing the current weather conditions in San Francisco,
│ ┃           Don't tell me about the structure or content of the file,
│ ┃           Briefly, using the weather information provided in the file, tell me if I need to wear a jacket today.
│ ┃
│
│ ✔ File.digest(excludeMetadata: true): String! 0.0s
│
│ ✔ File.withName(name: "San Francisco Weather"): File! 0.2s
│
│ ✔ copy /app/weather.txt /San Francisco Weather 0.2s
│🤖 It's cold today in San Francisco, so I recommend wearing a jacket.
```
