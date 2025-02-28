import dagger
from dagger import dag, function, object_type


@object_type
class MultiagentDemo:
    @function
    async def demo(
        self,
        chat_model: str = "gpt-4o",
        coder_model: str = "gpt-o1",
    ) -> str:
        """Demo of a multi-agent system"""

        ws = dag.toy_workspace()

        # write a program that gets the current weather report
        coder = (
           dag.llm(model = coder_model)
            .with_toy_workspace(ws)
            .with_prompt_var("assignment",
                """
                write a program called weather
                that retrieves current weather in San Francisco from wttr.in
                and prints a short report about the temperature and precipitation
                to the console
                """)
            .with_prompt_file(dag.current_module().source().file("coder.txt"))
        )

        # save the report to a file
        current_weather = (
            coder.toy_workspace()
            .container()
            .with_exec(["go", "run", "."], redirect_stdout="weather.txt")
            .file("weather.txt")
        )

        # give the report to another llm
        summarizer = (
            dag.llm(model = chat_model)
            .with_file(current_weather)
            .with_prompt("""
            You have access to a file describing the current weather conditions in San Francisco,
            Don't tell me about the structure or content of the file,
            Briefly, using the weather information provided in the file, tell me if I need to wear a jacket today.
            """)
        )

        return await summarizer.last_reply()
