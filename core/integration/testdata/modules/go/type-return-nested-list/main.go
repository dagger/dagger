package main

type Test struct{}

type Mood string

const (
	Happy Mood = "HAPPY"
	Sad   Mood = "SAD"
)

type Pair struct {
	Left  string
	Right string
}

func (m *Test) Ints() [][]int {
	return [][]int{{1, 2, 3}, {2, 4, 6}, {3, 6, 9}}
}

func (m *Test) Strs() [][]string {
	return [][]string{{"a", "b"}, {"c", "d"}}
}

func (m *Test) Bools() [][]bool {
	return [][]bool{{true, false}, {false, true}}
}

func (m *Test) Moods() [][]Mood {
	return [][]Mood{{Happy, Sad}, {Sad, Sad}}
}

func (m *Test) Pairs() [][]*Pair {
	return [][]*Pair{{{Left: "a", Right: "b"}}, {{Left: "c", Right: "d"}, {Left: "e", Right: "f"}}}
}

func (m *Test) Echo(v [][]int) [][]int {
	return v
}
