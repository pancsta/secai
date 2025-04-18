package schema

import (
	"context"

	"github.com/invopop/jsonschema"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/secai"
	ss "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	websearch "github.com/pancsta/secai/tools/searxng/schema"
)

var Sp = shared.Sp

// ///// ///// /////

// ///// STATES

// ///// ///// /////

// ResearchStatesDef contains all the states of the Research state machine.
type ResearchStatesDef struct {
	*am.StatesBase

	CheckingInfo string
	NeedMoreInfo string

	SearchingLLM string
	SearchingWeb string
	Scraping     string

	Answering string
	Answered  string

	// TODO parse number as a ref to suggested questions
	// CheckingRefs string
	// RefsChecked  string

	*ss.AgentStatesDef
}

// ResearchGroupsDef contains all the state groups Research state machine.
type ResearchGroupsDef struct {
	Info    S
	Search  S
	Answers S
}

// ResearchSchema represents all relations and properties of ResearchStates.
var ResearchSchema = SchemaMerge(
	// inherit from Agent
	ss.AgentSchema,

	am.Schema{

		// Choice "agent"
		ssR.CheckingInfo: {
			Require: S{ssR.Start, ssR.Prompt},
			Remove:  sgR.Info,
		},
		ssR.NeedMoreInfo: {
			Require: S{ssR.Start},
			Add:     S{ssR.SearchingLLM},
			Remove:  sgR.Info,
		},

		// Query "agent"
		ssR.SearchingLLM: {
			Require: S{ssR.NeedMoreInfo, ssR.Prompt},
			Remove:  sgR.Search,
		},
		ssR.SearchingWeb: {
			Require: S{ssR.NeedMoreInfo, ssR.Prompt},
			Remove:  sgR.Search,
		},
		ssR.Scraping: {
			Require: S{ssR.NeedMoreInfo, ssR.Prompt},
			Remove:  sgR.Search,
		},

		// Q&A "agent"
		ssR.Answering: {
			Require: S{ssR.Start, ssR.Prompt},
			Remove:  SAdd(sgR.Info, sgR.Answers),
		},
		ssR.Answered: {
			Require: S{ssR.Start},
			Remove:  SAdd(sgR.Info, sgR.Answers, S{ssR.Prompt}),
		},

		// OVERRIDES

		ssR.Interrupt: StateAdd(ss.AgentSchema[ss.AgentStates.Interrupt], State{
			// stop these from happening when interrupted
			Remove: S{ssR.CheckingInfo, ssR.SearchingLLM, ssR.SearchingWeb, ssR.Scraping},
		}),
		ssR.Prompt: StateAdd(ss.AgentSchema[ss.AgentStates.Prompt], State{
			// remove these when a new prompt is sent
			Remove: S{ssR.Answered},
		}),
	})

// EXPORTS AND GROUPS

var (
	ssR = am.NewStates(ResearchStatesDef{})
	sgR = am.NewStateGroups(ResearchGroupsDef{
		Info:    S{ssR.CheckingInfo, ssR.NeedMoreInfo},
		Search:  S{ssR.SearchingLLM, ssR.SearchingWeb, ssR.Scraping},
		Answers: S{ssR.Answering, ssR.Answered},
	})

	// ResearchStates contains all the states for the Research machine.
	ResearchStates = ssR
	// ResearchGroups contains all the state groups for the Research machine.
	ResearchGroups = sgR
)

// NewResearch will create the most basic Research state machine.
// TODO add to am-gen
func NewResearch(ctx context.Context) *am.Machine {
	return am.New(ctx, ResearchSchema, nil)
}

// ///// ///// /////

// ///// PROMPTS

// ///// ///// /////

func NewCheckingInfoPrompt(agent secai.AgentApi) *secai.Prompt[ParamsCheckingInfo, ResultCheckingInfo] {
	return secai.NewPrompt[ParamsCheckingInfo, ResultCheckingInfo](
		agent, ssR.CheckingInfo, `
			- You are a decision-making agent that determines whether a new web search is needed to answer the user's question.
			- Your primary role is to analyze whether the existing context contains sufficient, up-to-date information to
			answer the question.
			- You must output a clear TRUE/FALSE decision - TRUE if a new search is needed, FALSE if existing context is
			sufficient.
		`, `
			1. Analyze the user's question to determine whether or not an answer warrants a new search
			2. Review the available web search results 
			3. Determine if existing information is sufficient and relevant
			4. Make a binary decision: TRUE for new search, FALSE for using existing context
		`, `
			Your reasoning must clearly state WHY you need or don't need new information
			If the web search context is empty or irrelevant, always decide TRUE for new search
			If the question is time-sensitive, check the current date to ensure context is recent
			For ambiguous cases, prefer to gather fresh information
			Your decision must match your reasoning - don't contradict yourself
		`)
}

func NewSearchingLLMPrompt(agent secai.AgentApi) *secai.Prompt[ParamsSearching, ResultSearching] {
	return secai.NewPrompt[ParamsSearching, ResultSearching](
		agent, ssR.SearchingLLM, `
			- You are an expert search engine query generator with a deep understanding of which queries will maximize the
			number of relevant results.
		`, `
			1. Analyze the given instruction to identify key concepts and aspects that need to be researched
			2. For each aspect, craft a search query using appropriate search operators and syntax
			3. Ensure queries cover different angles of the topic (technical, practical, comparative, etc.)
		`, `
			Return exactly the requested number of queries
			Format each query like a search engine query, not a natural language question
			Each query should be a concise string of keywords and operators
		`)
}

func NewAnsweringPrompt(agent secai.AgentApi) *secai.Prompt[ParamsAnswering, ResultAnswering] {
	return secai.NewPrompt[ParamsAnswering, ResultAnswering](
		agent, ssR.Answering, `
			- You are an expert question answering agent focused on providing factual information and encouraging deeper topic
			exploration.
			- For general greetings or non-research questions, provide relevant information about the system's capabilities and
			research functions.
		`, `
			1. Analyze the question and identify the core topic
			2. Answer the question using available information
			3. For topic-specific questions, generate follow-up questions that explore deeper aspects of the same topic
			4. For general queries about the system, suggest questions about research capabilities and functionality
		`, `
			Answer in a direct, informative manner
			NEVER generate generic conversational follow-ups like 'How are you?' or 'What would you like to know?'
			For topic-specific questions, follow-up questions MUST be about specific aspects of that topic
			For system queries, follow-up questions should be about specific research capabilities
			Example good follow-ups for a Nobel Prize question:
			- What specific discoveries led to their Nobel Prize?
			- How has their research influenced their field?
			- What other scientists collaborated on this research?
			Example good follow-ups for system queries:
			- What types of sources do you use for research?
			- How do you verify information accuracy?
			- What are the limitations of your search capabilities?
		`)
}

// CheckingInfo (Choice "agent")

type ParamsCheckingInfo struct {
	UserMessage  string
	DecisionType string
}

type ResultCheckingInfo struct {
	Reasoning string `jsonschema:"description=Detailed explanation of the decision-making process"`
	Decision  bool   `jsonschema:"description=The final decision based on the analysis"`
}

// Searching (Query "agent")

type ParamsSearching struct {
	Instruction string `jsonschema:"description=A detailed instruction or request to generate search engine queries for."`
	NumQueries  int    `jsonschema:"description=The number of search queries to generate."`
}

type ResultSearching = websearch.Params

// Answering (Q&A "agent")

type ParamsAnswering struct {
	Question string `jsonschema:"description=The question to answer."`
}

type ResultAnswering struct {
	Answer            string `jsonschema:"description=The answer to the question."`
	FollowUpQuestions FollowUpQuestions
}

// nested result type & schema

type FollowUpQuestions []string

func (FollowUpQuestions) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:  "array",
		Items: &jsonschema.Schema{Type: "string"},
		Title: "Follow-up Questions",
		Description: Sp(`
			Specific questions about the topic that would help the user learn more details about the subject matter.
			For example, if discussing a Nobel Prize winner, suggest questions about their research, impact, or related
			scientific concepts.
		`),
	}
}
