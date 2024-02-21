import dagger
from dagger import function

@function
def hello() -> str:
  	return "Hello, world"
