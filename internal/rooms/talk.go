package rooms

// talkContract is the client-side instruction that rides the room's first
// message. The gateway's constitutional prompt only tells members to cite a
// peer to open the next round, so members default to talking to each other;
// this contract says the owner is the audience, a question to them ends the
// turn, and a request for a conversation is not an order for a file.
//
// It is instruction, not enforcement: the client shapes text and cannot stop
// the gateway from running a round a mention requests.
const talkContract = `[kontrakt rozmowy pokoju — obowiązuje wszystkich członków]
1. właściciel jest uczestnikiem tej rozmowy: decyzje, brakujące liczby i dostępy bierzesz od niego, nie od kolegów z pokoju.
2. jeśli do odpowiedzi brakuje ci decyzji, liczby albo dostępu — zadaj właścicielowi JEDNO konkretne pytanie wprost („Kamil, …?”) i zakończ turę; w tej turze bez plików i bez HANDOFF.
3. nie zaczepiaj @kolegi, żeby otworzyć kolejną rundę, chyba że właściciel poprosił o debatę między wami — wzmianka @handle otwiera następną rundę, więc bez jego zgody skracaj, nie przedłużaj.
4. prośba o rozmowę, brainstorming albo plan to rozmowa, nie zamówienie: pytania do właściciela, nie dokumenty. Pliki zapisuj tylko wtedy, gdy prosi o konkretny wytwór.
5. nie domykaj sprawy za niego: gdy czekasz na jego odpowiedź, powiedz to wprost („czekam na twoją decyzję w sprawie X”).`

// DiscussionBlock renders the room's talk contract. It is appended to the
// first message of every room, whether or not there is a project orientation:
// a room without a project still has an owner.
func DiscussionBlock() string {
	return talkContract
}
