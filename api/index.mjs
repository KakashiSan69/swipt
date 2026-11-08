const outside = () => {
  throw new Error(
    "@swipt/api runs inside Swipt; build the swiplet with `swipt project build`",
  );
};

export const available = outside;
