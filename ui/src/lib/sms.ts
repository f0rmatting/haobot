// Client-side estimate of how many SMS a text takes, mirroring the agent's
// encoder: GSM 7-bit when every character is in the GSM alphabet, else UCS-2.

const gsmBasic =
  "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà";
const gsmExtended = "^{}\\[~]|€\f";

export function smsParts(text: string): { parts: number; chars: number; encoding: "GSM" | "UCS-2"; perPart: number } {
  let gsm = true;
  let septets = 0;
  for (const ch of text) {
    if (gsmBasic.includes(ch)) septets += 1;
    else if (gsmExtended.includes(ch)) septets += 2;
    else {
      gsm = false;
      break;
    }
  }
  if (gsm) {
    const parts = septets === 0 ? 0 : septets <= 160 ? 1 : Math.ceil(septets / 153);
    return { parts, chars: septets, encoding: "GSM", perPart: parts > 1 ? 153 : 160 };
  }
  const units = text.length; // UTF-16 code units, as UCS-2 counts them
  const parts = units <= 70 ? 1 : Math.ceil(units / 67);
  return { parts, chars: units, encoding: "UCS-2", perPart: parts > 1 ? 67 : 70 };
}

// isPersonalNumber is true for numbers people reply to; short codes and
// service numbers (95588, 106…) often reject or charge for replies.
export function isPersonalNumber(peer: string): boolean {
  return /^1[3-9]\d{9}$/.test(peer) || /^\+\d{6,}$/.test(peer) || /^0\d{9,11}$/.test(peer);
}
