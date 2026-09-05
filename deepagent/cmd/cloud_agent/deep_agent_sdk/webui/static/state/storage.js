export function readStorage(storage, key) {
  try {
    return storage?.getItem(key) || "";
  } catch {
    return "";
  }
}

export function writeStorage(storage, key, value) {
  try {
    storage?.setItem(key, value);
  } catch {
    return;
  }
}
