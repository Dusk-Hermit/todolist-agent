package main

func (a *App) bindingGetState() StatePayload { return a.snapshot() }

func (a *App) bindingAddTodo(in TodoInput) (Todo, error) {
	return a.addTodo(in, "user", "user")
}

func (a *App) bindingUpdateTodo(id string, patch TodoPatch) (Todo, error) {
	return a.updateTodo(id, patch, "user")
}

func (a *App) bindingDeleteTodo(id string) (map[string]any, error) {
	if err := a.deleteTodo(id, "user"); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) bindingSetTodoStatus(id, status string) (Todo, error) {
	return a.setTodoStatus(id, status, "user")
}

func (a *App) bindingMoveTodo(id, priority, beforeID string, after bool) (map[string]any, error) {
	t, err := a.moveTodo(id, priority, beforeID, after, "user")
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "todo": t}, nil
}

func (a *App) bindingSetFocus(id string) (map[string]any, error) {
	focus, err := a.setFocus(id, "user")
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "focusId": focus}, nil
}

func (a *App) bindingJournal(beforeAt int64, limit int, query string) JournalPage {
	return a.journalPage(beforeAt, limit, query)
}
