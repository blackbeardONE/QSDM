type Field<T> = {
  property: keyof T | string;
  span: number;
  encode(value: any, buffer: Buffer, offset: number): void;
};

export function u8<T = any>(property: keyof T | string): Field<T> {
  return {
    property,
    span: 1,
    encode(value: any, buffer: Buffer, offset: number) {
      buffer.writeUInt8(Number(value[property as string] || 0), offset);
    },
  };
}

export function struct<T>(fields: Array<Field<T>>) {
  const span = fields.reduce((total, field) => total + field.span, 0);
  return {
    span,
    encode(value: T, buffer: Buffer) {
      let offset = 0;
      fields.forEach((field) => {
        field.encode(value, buffer, offset);
        offset += field.span;
      });
    },
  };
}
