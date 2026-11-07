const outside = () => {
  throw new Error(
    "@swipt/api/http runs inside Swipt; build the swiplet with `swipt project build`",
  );
};

export const Client = outside;
export const CookieJar = outside;
