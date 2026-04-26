package domain

import "time"

type Project struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	GitURL             string    `json:"gitUrl"`
	DefaultBranch      string    `json:"defaultBranch"`
	WorktreeNamePrefix string    `json:"worktreeNamePrefix"`
	Archived           bool      `json:"archived"`
	Version            int       `json:"version"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`

	pendingEvents []DomainEvent
}

type NewProjectInput struct {
	ID                 string
	Name               string
	GitURL             string
	DefaultBranch      string
	WorktreeNamePrefix string
	Now                time.Time
}

func NewProject(input NewProjectInput) (*Project, error) {
	if err := requireNonBlank("project id", input.ID); err != nil {
		return nil, err
	}
	if err := requireNonBlank("project name", input.Name); err != nil {
		return nil, err
	}
	if err := requireNonBlank("git url", input.GitURL); err != nil {
		return nil, err
	}
	if input.DefaultBranch == "" {
		input.DefaultBranch = "main"
	}
	if input.WorktreeNamePrefix == "" {
		input.WorktreeNamePrefix = input.ID
	}
	project := &Project{
		ID:                 input.ID,
		Name:               input.Name,
		GitURL:             input.GitURL,
		DefaultBranch:      input.DefaultBranch,
		WorktreeNamePrefix: input.WorktreeNamePrefix,
		Version:            1,
		CreatedAt:          input.Now,
		UpdatedAt:          input.Now,
	}
	project.addEvent("ProjectCreated", map[string]any{"name": project.Name}, input.Now)
	return project, nil
}

func (p *Project) Update(name, gitURL, defaultBranch, prefix string, now time.Time) error {
	if err := requireNonBlank("project name", name); err != nil {
		return err
	}
	if err := requireNonBlank("git url", gitURL); err != nil {
		return err
	}
	p.Name = name
	p.GitURL = gitURL
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	if prefix == "" {
		prefix = p.ID
	}
	p.DefaultBranch = defaultBranch
	p.WorktreeNamePrefix = prefix
	p.touch(now)
	p.addEvent("ProjectUpdated", map[string]any{"name": p.Name}, now)
	return nil
}

func (p *Project) Archive(now time.Time) {
	p.Archived = true
	p.touch(now)
	p.addEvent("ProjectArchived", nil, now)
}

func (p *Project) PullEvents() []DomainEvent {
	events := append([]DomainEvent(nil), p.pendingEvents...)
	p.pendingEvents = nil
	return events
}

func (p *Project) RestoreEvents(events []DomainEvent) {
	p.pendingEvents = append([]DomainEvent(nil), events...)
}

func (p *Project) touch(now time.Time) {
	p.Version++
	p.UpdatedAt = now
}

func (p *Project) addEvent(eventType string, payload any, now time.Time) {
	p.pendingEvents = append(p.pendingEvents, newEvent(eventType, "Project", p.ID, p.Version, payload, now))
}
