import { createAPI } from "./api/client.js";
import { mountApp } from "./components/app_shell.js";
import { createFeatures } from "./features/index.js";
import { initialState, reduce } from "./state/reducer.js";
import { createStore } from "./state/store.js";

const api = createAPI();
const store = createStore(reduce, initialState());
const features = createFeatures({ api, store });
const app = mountApp(document.querySelector("#app"), {
  store,
  actions: features.actions,
});

app.start();
features.start().catch((error) => {
  store.dispatch({ type: "ui/error", error: error.message });
});
