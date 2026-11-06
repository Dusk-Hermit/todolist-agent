(() => {
  "use strict";
  document.documentElement.classList.add("android");
  window.todo_openUpdatePage = () => TodoNative.openUpdatePage();
  const methods = [
    "ready", "getState", "addTodo", "updateTodo", "setTodoStatus",
    "deleteTodo", "moveTodo", "setFocus", "journal", "sendInput", "savePrefs",
    "lanState", "lanConfigure", "lanRefresh", "lanSelect",
    "updateState", "updateCheck", "updateInstall", "updateAuto",
  ];
  let seq = 0;
  const pending = new Map();
  window.androidReply = (id, response) => {
    const done = pending.get(id);
    if (!done) return;
    pending.delete(id);
    if (response.error) done.reject(new Error(response.error));
    else done.resolve(response.value);
  };
  for (const method of methods) {
    const name = "todo_" + method;
    window[name] = (...args) => new Promise((resolve, reject) => {
      const id = ++seq;
      pending.set(id, { resolve, reject });
      TodoNative.invoke(id, name, JSON.stringify(args));
    });
  }
})();
