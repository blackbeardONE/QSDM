export default class ElectronStoreMock {
  private data = new Map<string, unknown>();

  get(key: string) {
    return this.data.get(key);
  }

  set(key: string, value: unknown) {
    this.data.set(key, value);
  }

  delete(key: string) {
    this.data.delete(key);
  }
}
