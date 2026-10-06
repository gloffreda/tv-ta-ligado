// localStorage com try/catch (modo privado, bloqueado, sem armazenamento).
export function load(key: string, def: string): string {
  try {
    const v = window.localStorage.getItem(key);
    return v === null ? def : v;
  } catch {
    return def;
  }
}

export function save(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    /* sem armazenamento: segue com o padrão */
  }
}
