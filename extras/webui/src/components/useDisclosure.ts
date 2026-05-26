import { useEffect, useRef, useState } from "react";

export function useDisclosure(autoOpen: boolean): [boolean, () => void] {
  const userTouched = useRef(false);
  const [open, setOpen] = useState(autoOpen);

  useEffect(() => {
    if (!userTouched.current) setOpen(autoOpen);
  }, [autoOpen]);

  function toggleOpen() {
    userTouched.current = true;
    setOpen((current) => !current);
  }

  return [open, toggleOpen];
}
