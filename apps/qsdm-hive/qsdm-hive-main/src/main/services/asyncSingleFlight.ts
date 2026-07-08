export class AsyncSingleFlight<T> {
  private inFlight: Promise<T> | null = null;

  get isRunning() {
    return this.inFlight !== null;
  }

  async run(operation: () => Promise<T>): Promise<T> {
    if (this.inFlight) {
      return this.inFlight;
    }

    const operationPromise = operation();
    this.inFlight = operationPromise;
    try {
      return await operationPromise;
    } finally {
      if (this.inFlight === operationPromise) {
        this.inFlight = null;
      }
    }
  }
}
