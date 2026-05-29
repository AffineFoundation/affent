package taskstate

type Snapshot struct {
	Objective         string     `json:"objective,omitempty"`
	Status            string     `json:"status,omitempty"`
	CurrentStep       string     `json:"current_step,omitempty"`
	RequestMode       string     `json:"request_mode,omitempty"`
	RequestSource     string     `json:"request_source,omitempty"`
	ScheduleID        string     `json:"schedule_id,omitempty"`
	ScheduleKind      string     `json:"schedule_kind,omitempty"`
	Constraints       []string   `json:"constraints,omitempty"`
	KnownFacts        []string   `json:"known_facts,omitempty"`
	ChangedFiles      []File     `json:"changed_files,omitempty"`
	AttemptedActions  []Action   `json:"attempted_actions,omitempty"`
	FailedActions     []Failure  `json:"failed_actions,omitempty"`
	Evidence          []Evidence `json:"evidence,omitempty"`
	VerificationState string     `json:"verification_state,omitempty"`
	OpenQuestions     []string   `json:"open_questions,omitempty"`
	NextStep          string     `json:"next_step,omitempty"`
	Sources           []string   `json:"sources,omitempty"`
}

type File struct {
	Path   string `json:"path"`
	Action string `json:"action,omitempty"`
}

type Action struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary,omitempty"`
	TurnID  string `json:"turn_id,omitempty"`
	CallID  string `json:"call_id,omitempty"`
}

type Failure struct {
	Tool    string   `json:"tool"`
	Summary string   `json:"summary,omitempty"`
	Kinds   []string `json:"kinds,omitempty"`
	Next    string   `json:"next,omitempty"`
	TurnID  string   `json:"turn_id,omitempty"`
	CallID  string   `json:"call_id,omitempty"`
}

type Evidence struct {
	Source  string `json:"source"`
	Summary string `json:"summary,omitempty"`
	TurnID  string `json:"turn_id,omitempty"`
	CallID  string `json:"call_id,omitempty"`
}

func CloneSnapshotPtr(in Snapshot) *Snapshot {
	if IsEmpty(in) {
		return nil
	}
	out := in
	out.Constraints = append([]string(nil), in.Constraints...)
	out.KnownFacts = append([]string(nil), in.KnownFacts...)
	out.ChangedFiles = append([]File(nil), in.ChangedFiles...)
	out.AttemptedActions = append([]Action(nil), in.AttemptedActions...)
	out.FailedActions = append([]Failure(nil), in.FailedActions...)
	for i := range out.FailedActions {
		out.FailedActions[i].Kinds = append([]string(nil), in.FailedActions[i].Kinds...)
	}
	out.Evidence = append([]Evidence(nil), in.Evidence...)
	out.OpenQuestions = append([]string(nil), in.OpenQuestions...)
	out.Sources = append([]string(nil), in.Sources...)
	return &out
}

func IsEmpty(task Snapshot) bool {
	return task.Objective == "" &&
		(task.Status == "" || task.Status == "unknown") &&
		task.CurrentStep == "" &&
		task.RequestMode == "" &&
		task.RequestSource == "" &&
		task.ScheduleID == "" &&
		task.ScheduleKind == "" &&
		len(task.Constraints) == 0 &&
		len(task.KnownFacts) == 0 &&
		len(task.ChangedFiles) == 0 &&
		len(task.AttemptedActions) == 0 &&
		len(task.FailedActions) == 0 &&
		len(task.Evidence) == 0 &&
		(task.VerificationState == "" || task.VerificationState == "unknown") &&
		len(task.OpenQuestions) == 0 &&
		task.NextStep == "" &&
		len(task.Sources) == 0
}
